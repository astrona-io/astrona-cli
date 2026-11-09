package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/labops"
	"astrona/internal/outbox"
)

// Everything the CLI tells Astrona about a running lab goes through the
// outbox; queueing and flushing live in internal/labops. What stays here is
// its hidden command.

// signalTimeout bounds one attempt to reach the site.
const signalTimeout = labops.SignalTimeout

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

// startOutboxFlusher starts a detached `astrona outbox-flush` when signals
// are waiting (labops.StartOutboxFlusher).
func startOutboxFlusher() { labops.StartOutboxFlusher() }

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
