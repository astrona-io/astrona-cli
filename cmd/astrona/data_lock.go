package main

import (
	"fmt"
	"strings"
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

// lockFreeCommand reports whether cmd runs without the shared data lock.
// Only commands that provably never read or write what data clean and data
// reset remove (the disposable caches, settings, the current selection,
// catalog and update-check metadata) belong here. The root hook skips
// settings, the selection and the update check for these hidden commands.
// The detached helpers run for as long as a lab lives; holding the lock
// would keep cleanup from ever running.
//
//   - usage-reporter: lab state, VM handles, engine stats, credentials.
//   - outbox-flush: the outbox and credentials.
//   - port-forward supervise: its own forward spec and status, kubectl.
//   - playground-watchdog: lab state while waiting; it takes the shared lock
//     itself around the destroy at the deadline (destroyPlayground).
//   - schema, docgen, help and completion: no local data at all.
//
// Every other command, hidden or not, holds the shared lock.
func lockFreeCommand(cmd *cobra.Command) bool {
	switch strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ") {
	case "usage-reporter", "outbox-flush", "port-forward supervise", "playground-watchdog",
		"schema", "docgen", "help", "__complete", "__completeNoDesc":
		return true
	}
	return false
}

// executeWithDataLock holds a shared lock before command setup until Execute
// returns. Data commands acquire their own exclusive lock only when applying.
// Terms use an independent atomic receipt and can be read without any writes.
// Installing this at the entrypoint keeps command construction side-effect free.
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
			if release == nil && !lockFreeCommand(c) {
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
