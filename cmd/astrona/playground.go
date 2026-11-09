package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/account"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// A playground started from the catalog is timed (internal/labops,
// playground.go): its clock starts with `astrona run` and stops at
// `astrona destroy`, or at its time limit, when the detached watchdog
// removes the cluster. What stays here is the watchdog's hidden command,
// destroying the playground, and the wrappers cmd's callers use.

// isPlaygroundName reports whether a catalog name is a module's playground.
func isPlaygroundName(name string) bool { return labops.IsPlaygroundName(name) }

// startPlaygroundWatchdog starts the detached process that ends the
// playground at its deadline and says when that is.
func startPlaygroundWatchdog(clusterName string, sess *labstate.Session) {
	labops.StartPlaygroundWatchdog(os.Stdout, os.Stderr, clusterName, sess)
}

func newPlaygroundWatchdogCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "playground-watchdog <cluster>",
		Short:  "Stop a playground at its time limit (started by astrona run)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return watchPlayground(ctx, args[0], time.Minute)
		},
	}
}

// watchPlayground waits until the playground's deadline, then stops its
// clock and destroys it (labops.WatchPlayground). A signal ends it quietly.
func watchPlayground(ctx context.Context, clusterName string, tick time.Duration) error {
	return labops.WatchPlayground(ctx, os.Stdout, os.Stderr, clusterName, tick, destroyPlayground)
}

// destroyPlayground destroys a playground whose time limit was reached.
func destroyPlayground(clusterName string) error {
	rep, err := ui.NewReporter("destroy", clusterName, false)
	if err != nil {
		return err
	}
	defer rep.Close()
	return destroyByName(clusterName, rep)
}

// stopPlaygroundClock tells the site the playground ended, through the
// outbox (labops.StopPlaygroundClock).
func stopPlaygroundClock(ctx context.Context, sess *labstate.Session, reason string, usage *account.UsageReport) {
	labops.StopPlaygroundClock(ctx, os.Stderr, sess, reason, usage)
}

// endRunSession runs when a lab is forgotten (labops.EndRunSession).
func endRunSession(clusterName string) { labops.EndRunSession(os.Stderr, clusterName) }

// endOldSession runs before a lab is built again under the same name
// (labops.EndOldSession).
func endOldSession(clusterName string) { labops.EndOldSession(os.Stderr, clusterName) }

// stopWatchdog ends clusterName's playground watchdog — unless that is us
// (labops.StopWatchdog).
func stopWatchdog(pid int, clusterName string) { labops.StopWatchdog(pid, clusterName) }
