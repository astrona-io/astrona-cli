package main

import (
	"fmt"
	"time"

	"astrona/internal/localdata"
	"github.com/spf13/cobra"
)

// dataLockWait bounds how long a command waits for local data that another
// command (a cleanup, or the commands a cleanup waits for) is using.
var dataLockWait = 5 * time.Second

// lockLocalData takes the maintenance lock, waiting up to dataLockWait and
// saying so on stderr, so JSON stdout stays clean.
func lockLocalData(cmd *cobra.Command, s localdata.Store, exclusive bool) (func(), error) {
	return s.LockWait(cmd.Context(), exclusive, dataLockWait, func() {
		fmt.Fprintln(cmd.ErrOrStderr(), "Waiting for another Astrona command to finish with local data...")
	})
}

// commandHidden reports whether cmd is, or sits under, a hidden command:
// internal helpers such as usage-reporter, outbox-flush, playground-watchdog
// and port-forward supervise.
func commandHidden(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden {
			return true
		}
	}
	return false
}

// executeWithDataLock holds a shared lock before command setup until Execute
// returns. Data commands acquire their own exclusive lock only when applying.
// Terms use an independent atomic receipt and can be read without any writes.
// Hidden helpers run detached for as long as a lab lives and never touch the
// disposable caches, so they take no lock; otherwise they would keep cleanup
// from ever running. Installing this at the entrypoint keeps command
// construction side-effect free.
func executeWithDataLock(root *cobra.Command, store func() (localdata.Store, error)) error {
	var release func()
	var ready *cobra.Command
	var dataStore localdata.Store
	defer func() {
		if release != nil {
			release()
		}
	}()
	var wrap func(*cobra.Command, func(*cobra.Command, []string) error)
	wrap = func(cmd *cobra.Command, inherited func(*cobra.Command, []string) error) {
		// Settings writers share settings.json.lock with data reset. Read-only
		// settings commands must not create the maintenance directory or lock.
		if cmd.Name() == "data" || cmd.Name() == "terms" || cmd.Name() == "settings" {
			return
		}
		before := inherited
		if cmd.PersistentPreRunE != nil {
			before = cmd.PersistentPreRunE
		} else if cmd.PersistentPreRun != nil {
			plain := cmd.PersistentPreRun
			before = func(c *cobra.Command, args []string) error { plain(c, args); return nil }
		}
		cmd.PersistentPreRunE = func(c *cobra.Command, args []string) error {
			if release == nil && !commandHidden(c) && c.Name() != "help" && c.Name() != "docgen" && c.Name() != "__complete" && c.Name() != "__completeNoDesc" {
				s, err := store()
				if err != nil {
					return err
				}
				release, err = lockLocalData(c, s, false)
				if err != nil {
					return err
				}
				dataStore = s
			}
			if before != nil {
				if err := before(c, args); err != nil {
					return err
				}
			}
			ready = c
			return nil
		}
		for _, child := range cmd.Commands() {
			wrap(child, before)
		}
	}
	wrap(root, nil)
	err := root.Execute()
	if release != nil {
		release()
		release = nil
		if ready != nil {
			enforceCachePolicy(ready, dataStore)
		}
	}
	return err
}
