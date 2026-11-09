package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// While a signed-in lab or playground runs, a detached `astrona
// usage-reporter <cluster>` posts what it uses on this machine to the lab
// session (labops.ReportUsage). What stays here is its hidden command and
// the measuring, which reads the same container engine and QEMU data as
// the dashboard.

func newUsageReporterCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "usage-reporter <cluster>",
		Short:  "Send a running lab's CPU and memory to its lab session (started by astrona run)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return labops.ReportUsage(ctx, os.Stdout, args[0], labops.UsageReportInterval, sampleLabUsage)
		},
	}
}

// sampleLabUsage measures the lab once (recorded locally too, at most one
// sample per five seconds, shared with the dashboard). ok is false when the
// lab could not be measured as a whole.
var sampleLabUsage labops.UsageSampler = func(ctx context.Context, clusterName string, st *labstate.State) (labstate.ResourceUsage, bool) {
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
