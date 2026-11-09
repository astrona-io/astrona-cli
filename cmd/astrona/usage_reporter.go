package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"astrona/internal/labops"
)

// While a signed-in lab or playground runs, a detached `astrona
// usage-reporter <cluster>` posts what it uses on this machine to the lab
// session (labops.ReportUsage, measured by labops.SampleLabUsage). What
// stays here is its hidden command.

func newUsageReporterCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "usage-reporter <cluster>",
		Short:  "Send a running lab's CPU and memory to its lab session (started by astrona run)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return labops.ReportUsage(ctx, os.Stdout, args[0], labops.UsageReportInterval, labops.SampleLabUsage)
		},
	}
}
