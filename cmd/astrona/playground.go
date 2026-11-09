package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/labops"
	"astrona/internal/localdata"
	"astrona/internal/ui"
)

// A playground started from the catalog is timed (internal/labops,
// playground.go): its clock starts with `astrona run` and stops at
// `astrona destroy`, or at its time limit, when the detached watchdog
// removes the cluster. What stays here is the watchdog's hidden command,
// destroying the playground, and the callbacks cmd hands to labops.

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

// watchdogLockWait bounds how long the watchdog waits for a data cleanup or
// reset to finish before destroying. It runs detached, so nobody waits on it.
const watchdogLockWait = time.Minute

// destroyPlayground destroys a playground whose time limit was reached. The
// watchdog holds no data lock while it waits, but destroy may load the lab's
// config from a cached checkout, so it takes the shared lock like any other
// command for the destroy itself.
func destroyPlayground(clusterName string) error {
	s, err := localdata.DefaultStore()
	if err != nil {
		return err
	}
	release, err := s.LockWait(context.Background(), false, watchdogLockWait, nil)
	if err != nil {
		return err
	}
	defer release()
	rep, err := ui.NewReporter("destroy", clusterName, false)
	if err != nil {
		return err
	}
	defer rep.Close()
	return labops.DestroyByName(clusterName, rep, destroyOptions())
}

// endRunSession runs when a lab is forgotten (labops.EndRunSession).
func endRunSession(clusterName string) { labops.EndRunSession(os.Stderr, clusterName) }
