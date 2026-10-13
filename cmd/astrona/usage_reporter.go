package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/account"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// While a signed-in lab or playground runs, a detached `astrona
// usage-reporter <cluster>` measures what its kind cluster (and linked
// clusters) or QEMU VMs use on this machine and posts it to the lab session
// every 15 seconds, so the site knows the real cost of a lab. It follows the
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

// sendLabUsage posts samples to sess on its site. Replaced in tests.
var sendLabUsage = func(ctx context.Context, sess *labstate.Session, samples []account.UsageSample) (int, error) {
	client, store, _, err := accountDeps()
	if err != nil {
		return 0, err
	}
	creds, err := client.Active(ctx, store, sess.Site)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return client.SendLabUsage(ctx, store, creds, sess.ID, samples)
}

// reportUsage samples clusterName every tick and posts what it measured.
// Samples that could not be sent wait for the next post (the newest
// account.MaxUsageSamplesPerPost of them).
func reportUsage(ctx context.Context, clusterName string, tick time.Duration) error {
	var pending []account.UsageSample
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
		if u, ok := sampleLabUsage(sampleCtx, clusterName, st); ok && u.Updated.After(last) {
			last = u.Updated
			pending = append(pending, account.UsageSample{At: u.Updated.UTC(), CPUCores: u.CPU / 100, MemoryBytes: int64(u.Memory)})
			if len(pending) > account.MaxUsageSamplesPerPost {
				pending = pending[len(pending)-account.MaxUsageSamplesPerPost:]
			}
		}
		cancel()
		if len(pending) > 0 {
			kept, err := sendLabUsage(ctx, st.Session, pending)
			switch {
			case err == nil:
				pending = nil
				if idle = idle + 1; kept > 0 {
					idle = 0
				}
				if idle >= usageIdlePosts {
					fmt.Printf("%s the site takes no more samples for %s; stopping\n", time.Now().Format(time.RFC3339), clusterName)
					return nil
				}
			case errors.Is(err, account.ErrSessionGone), errors.Is(err, account.ErrSignedOut):
				fmt.Printf("%s %s: %s; stopping\n", time.Now().Format(time.RFC3339), clusterName, err)
				return nil
			default:
				fmt.Printf("%s %s: %s; trying again\n", time.Now().Format(time.RFC3339), clusterName, err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tick):
		}
	}
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
