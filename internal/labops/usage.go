package labops

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

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
	// UsageReportInterval is how often the reporter samples a lab.
	UsageReportInterval = 15 * time.Second
	// Posts in a row in which the site kept nothing before the reporter ends.
	usageIdlePosts = 3
)

// StartUsageReporter starts the reporter for a lab that just became ready.
// A failure is only a notice on warn: the lab works the same without it.
func StartUsageReporter(warn io.Writer, clusterName string) {
	pid, err := SpawnUsageReporter(clusterName)
	if err != nil {
		ui.Fwarnf(warn, "couldn't start measuring the lab's CPU and memory (%s)", err)
		return
	}
	if err := labstate.Update(clusterName, func(s *labstate.State) {
		if s.Session != nil {
			s.Session.ReporterPID = pid
		}
	}); err != nil {
		ui.Fwarnf(warn, "could not remember the lab's usage reporter (%s)", err)
	}
}

// SpawnUsageReporter runs `astrona usage-reporter <cluster>` detached, with
// its output in a log next to the lab's state. Replaced in tests.
var SpawnUsageReporter = func(clusterName string) (int, error) {
	return SpawnDetached("usage-reporter", clusterName, ".usage.log")
}

// UsageSampler measures the lab once; ok is false when the lab could not be
// measured as a whole. The measuring itself (container engine, QEMU VMs)
// belongs to the caller.
type UsageSampler func(ctx context.Context, clusterName string, st *labstate.State) (labstate.ResourceUsage, bool)

// ReportUsage samples clusterName with sample every tick into the outbox and
// sends what waits there — its own samples and any signal kept while
// offline. It also pauses the run's clock when every lab window has gone
// quiet without saying so (a crash, a closed terminal), from their last
// heartbeat. Progress lines go to w. It is the body of the hidden `astrona
// usage-reporter` helper.
func ReportUsage(ctx context.Context, w io.Writer, clusterName string, tick time.Duration, sample UsageSampler) error {
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
		if finished, err := labstate.ReadSubmission(clusterName, st.StartedAt); err != nil || finished != nil {
			return err
		}
		// Another run of the lab started its own reporter.
		if pid := st.Session.ReporterPID; pid != 0 && pid != os.Getpid() {
			return nil
		}
		if exp, err := time.Parse(time.RFC3339, st.Session.ExpiresAt); err == nil && time.Now().After(exp) {
			return nil
		}
		sampleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		u, ok := sample(sampleCtx, clusterName, st)
		cancel()
		added := false
		if ok && u.Updated.After(last) {
			last = u.Updated
			s := account.UsageSample{At: u.Updated.UTC(), CPUCores: u.CPU / 100, MemoryBytes: int64(u.Memory)}
			if err := outbox.Add(st.Session.Site, st.Session.ID, outbox.Event{Kind: outbox.Usage, At: s.At, Sample: &s}); err != nil {
				fmt.Fprintf(w, "%s %s: %s\n", time.Now().Format(time.RFC3339), clusterName, err)
			} else {
				added = true
			}
		}
		pauseForQuietWindows(ctx, clusterName, st, time.Now())
		kept, status, err := SendUsage(ctx)
		switch {
		case err != nil:
			fmt.Fprintf(w, "%s %s: %s\n", time.Now().Format(time.RFC3339), clusterName, err)
		case status.State == outbox.SignedOut:
			fmt.Fprintf(w, "%s %s: signed out; samples wait for astrona login\n", time.Now().Format(time.RFC3339), clusterName)
		case status.State != outbox.Online:
			fmt.Fprintf(w, "%s %s: offline (%s); %d signals wait\n", time.Now().Format(time.RFC3339), clusterName, status.Error, status.Pending)
		case added:
			// The site keeps no more samples for a lab that passed: stop then.
			if idle = idle + 1; kept != 0 {
				idle = 0
			}
			if idle >= usageIdlePosts {
				fmt.Fprintf(w, "%s the site takes no more samples for %s; stopping\n", time.Now().Format(time.RFC3339), clusterName)
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

// SendUsage flushes the outbox and says how many samples the site kept in its
// last usage post (-1: none was sent). Replaced in tests.
var SendUsage = func(ctx context.Context) (int, outbox.Status, error) {
	sender, err := NewSiteSender()
	if err != nil {
		return -1, outbox.Status{}, err
	}
	counting := &keptCounter{SiteSender: sender, kept: -1}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status, err := outbox.Flush(ctx, counting)
	return counting.kept, status, err
}

// keptCounter remembers how many samples the site kept in the last post.
type keptCounter struct {
	*SiteSender
	kept int
}

func (k *keptCounter) Usage(ctx context.Context, site, session string, samples []account.UsageSample) error {
	cr, err := k.Active(ctx, site)
	if err != nil {
		return err
	}
	kept, err := k.Client.SendLabUsage(ctx, k.Store, cr, session, samples)
	if err == nil {
		k.kept = kept
	}
	return k.Classify(err)
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
	_, _, _ = SignalClock(ctx, clusterName, st.StartedAt, true, lastSeen)
}
