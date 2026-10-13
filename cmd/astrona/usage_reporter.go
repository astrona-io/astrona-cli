package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/account"
	"astrona/internal/labstate"
	"astrona/internal/outbox"
	"astrona/internal/ui"
)

// While a signed-in lab or playground runs, a detached `astrona
// usage-reporter <cluster>` measures what its kind cluster (and linked
// clusters) or QEMU VMs use on this machine and posts it to the lab session
// every 15 seconds, so the site knows the real cost of a lab. Samples go
// through the outbox (kept on disk while offline, sent in order later), and
// the reporter flushes it every tick — also for the lab window's pauses. It
// pauses the run's clock itself when the lab windows went quiet without
// saying so. It follows the
// lab's state: it ends when the lab is destroyed or started again, its
// session is gone or expired, or the site stops taking samples (a lab that
// passed). `astrona run renew` keeps it: it reads the new session id.

const (
	usageReportInterval = 15 * time.Second
	// Posts in a row in which the site kept nothing before the reporter ends.
	usageIdlePosts = 3
)

// startUsageReporter starts the reporter for a lab that just became ready.
// A failure is only a notice: the lab works the same without it.
func startUsageReporter(clusterName string) {
	pid, err := spawnUsageReporter(clusterName)
	if err != nil {
		ui.Warnf("couldn't start measuring the lab's CPU and memory (%s)", err)
		return
	}
	if err := labstate.Update(clusterName, func(s *labstate.State) {
		if s.Session != nil {
			s.Session.ReporterPID = pid
		}
	}); err != nil {
		ui.Warnf("could not remember the lab's usage reporter (%s)", err)
	}
}

// spawnUsageReporter runs `astrona usage-reporter <cluster>` detached, with
// its output in a log next to the lab's state. Replaced in tests.
var spawnUsageReporter = func(clusterName string) (int, error) {
	return spawnDetached("usage-reporter", clusterName, ".usage.log")
}

func newUsageReporterCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "usage-reporter <cluster>",
		Short:  "Send a running lab's CPU and memory to its lab session (started by astrona run)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return reportUsage(ctx, args[0], usageReportInterval)
		},
	}
}

// sampleLabUsage measures the lab once (recorded locally too, at most one
// sample per five seconds, shared with the dashboard). ok is false when the
// lab could not be measured as a whole.
var sampleLabUsage = func(ctx context.Context, clusterName string, st *labstate.State) (labstate.ResourceUsage, bool) {
	groups, _ := readUIQEMUVMs()
	if vms := groups[clusterName]; len(vms) > 0 {
		u := uiQEMURunUsage(clusterName, st.StartedAt, vms, readUIQEMUUsage(ctx, groups))
		return u, u.Samples > 0
	}
	containers, provider, err := readHomeEngine(ctx)
	if err != nil {
		return labstate.ResourceUsage{}, false
	}
	u := uiRunUsage(clusterName, st.StartedAt, containers, readUIContainerUsage(ctx, provider, containers))
	return u, u.Samples > 0
}

// reportUsage samples clusterName every tick into the outbox and sends what
// waits there — its own samples and any signal kept while offline. It also
// pauses the run's clock when every lab window has gone quiet without saying
// so (a crash, a closed terminal), from their last heartbeat.
func reportUsage(ctx context.Context, clusterName string, tick time.Duration) error {
	var last time.Time
	idle := 0
	for {
		st, err := labstate.Load(clusterName)
		if err != nil {
			return err
		}
		if st == nil || st.LocalOnly || st.Session == nil || st.StartedAt == "" {
			return nil
		}
		// Another run of the lab started its own reporter.
		if pid := st.Session.ReporterPID; pid != 0 && pid != os.Getpid() {
			return nil
		}
		if exp, err := time.Parse(time.RFC3339, st.Session.ExpiresAt); err == nil && time.Now().After(exp) {
			return nil
		}
		sampleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		u, ok := sampleLabUsage(sampleCtx, clusterName, st)
		cancel()
		added := false
		if ok && u.Updated.After(last) {
			last = u.Updated
			sample := account.UsageSample{At: u.Updated.UTC(), CPUCores: u.CPU / 100, MemoryBytes: int64(u.Memory)}
			if err := outbox.Add(st.Session.Site, st.Session.ID, outbox.Event{Kind: outbox.Usage, At: sample.At, Sample: &sample}); err != nil {
				fmt.Printf("%s %s: %s\n", time.Now().Format(time.RFC3339), clusterName, err)
			} else {
				added = true
			}
		}
		pauseForQuietWindows(ctx, clusterName, st, time.Now())
		kept, status, err := sendUsage(ctx)
		switch {
		case err != nil:
			fmt.Printf("%s %s: %s\n", time.Now().Format(time.RFC3339), clusterName, err)
		case status.State == outbox.SignedOut:
			fmt.Printf("%s %s: signed out; samples wait for astrona login\n", time.Now().Format(time.RFC3339), clusterName)
		case status.State != outbox.Online:
			fmt.Printf("%s %s: offline (%s); %d signals wait\n", time.Now().Format(time.RFC3339), clusterName, status.Error, status.Pending)
		case added:
			// The site keeps no more samples for a lab that passed: stop then.
			if idle = idle + 1; kept != 0 {
				idle = 0
			}
			if idle >= usageIdlePosts {
				fmt.Printf("%s the site takes no more samples for %s; stopping\n", time.Now().Format(time.RFC3339), clusterName)
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tick):
		}
	}
}

// sendUsage flushes the outbox and says how many samples the site kept in its
// last usage post (-1: none was sent). Replaced in tests.
var sendUsage = func(ctx context.Context) (int, outbox.Status, error) {
	sender, err := newSiteSender()
	if err != nil {
		return -1, outbox.Status{}, err
	}
	counting := &keptCounter{siteSender: sender, kept: -1}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status, err := outbox.Flush(ctx, counting)
	return counting.kept, status, err
}

// keptCounter remembers how many samples the site kept in the last post.
type keptCounter struct {
	*siteSender
	kept int
}

func (k *keptCounter) Usage(ctx context.Context, site, session string, samples []account.UsageSample) error {
	cr, err := k.active(ctx, site)
	if err != nil {
		return err
	}
	kept, err := k.client.SendLabUsage(ctx, k.store, cr, session, samples)
	if err == nil {
		k.kept = kept
	}
	return k.classify(err)
}

// pauseForQuietWindows pauses the run's clock from the last heartbeat when
// lab windows were open for it but none has been heard from for a while — the
// windows pause and resume themselves; this catches the ones that could not.
func pauseForQuietWindows(ctx context.Context, clusterName string, st *labstate.State, now time.Time) {
	lastSeen, seen, err := labstate.LastPresence(clusterName, st.StartedAt)
	if err != nil || !seen || now.Sub(lastSeen) < 30*time.Second {
		return
	}
	if active, err := labstate.HasActiveUI(clusterName, st.StartedAt, now); err != nil || active {
		return
	}
	if paused, err := labstate.RemotePaused(clusterName, st.StartedAt, st.Session.ID); err != nil || paused {
		return
	}
	_, _, _ = signalClock(ctx, clusterName, st.StartedAt, true, lastSeen)
}

// runUsageReport is the summary of what a run used so far, for submit and
// destroy; nil when it was never measured.
func runUsageReport(clusterName string, st *labstate.State) *account.UsageReport {
	if st == nil || st.StartedAt == "" {
		return nil
	}
	u, err := labstate.ReadUsage(clusterName, st.StartedAt)
	if err != nil || u.Samples == 0 {
		return nil
	}
	return &account.UsageReport{
		AverageCPUCores:    u.AverageCPU / 100,
		AverageMemoryBytes: int64(u.AverageMemory),
		PeakCPUCores:       u.PeakCPU / 100,
		PeakMemoryBytes:    int64(u.PeakMemory),
		Samples:            u.Samples,
	}
}
