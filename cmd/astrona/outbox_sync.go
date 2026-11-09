package main

import (
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/labops"
)

// Everything the CLI tells Astrona about a running lab goes through the
// outbox; queueing and flushing live in internal/labops. What stays here is
// its hidden command.

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
