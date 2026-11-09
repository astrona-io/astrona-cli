package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/account"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/outbox"
)

// Everything the CLI tells Astrona about a running lab goes through the
// outbox; queueing and flushing live in internal/labops. What stays here is
// starting the detached `astrona outbox-flush` helper and its command.

// signalTimeout bounds one attempt to reach the site.
const signalTimeout = labops.SignalTimeout

// siteSender sends outbox signals with the signed-in account of each site.
type siteSender = labops.SiteSender

func newSiteSender() (*siteSender, error) { return labops.NewSiteSender() }

// Clock sync outcomes for the lab window.
const (
	clockLocal   = labops.ClockLocal
	clockSame    = labops.ClockSame
	clockSent    = labops.ClockSent
	clockWaiting = labops.ClockWaiting
)

// signalClock tells Astrona the run's clock paused (or resumed) at at.
func signalClock(ctx context.Context, lab, started string, paused bool, at time.Time) (string, outbox.Status, error) {
	return labops.SignalClock(ctx, lab, started, paused, at)
}

// stopSession queues a stop for sess and tries to send it; offline, it
// warns on stderr once.
func stopSession(ctx context.Context, sess *labstate.Session, reason string, usage *account.UsageReport, what string) {
	labops.StopSession(ctx, os.Stderr, sess, reason, usage, what)
}

// startOutboxFlusher starts a detached `astrona outbox-flush` when signals are
// waiting, so they go out even when nothing else runs. It never blocks the
// command that starts it.
func startOutboxFlusher() {
	if n, err := outbox.Pending(); err != nil || n == 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir, err := outbox.Dir()
	if err != nil {
		return
	}
	logF, err := os.OpenFile(filepath.Join(dir, "flush.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer logF.Close()
	cmd := exec.Command(exe, "outbox-flush")
	cmd.Stdout, cmd.Stderr = logF, logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

func newOutboxFlushCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "outbox-flush",
		Short:  "Send lab signals kept while offline (started by astrona when some are waiting)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return labops.RunOutboxFlusher(cmd.Context(), 30*time.Minute)
		},
	}
}
