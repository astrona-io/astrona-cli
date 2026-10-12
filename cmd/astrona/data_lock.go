package main

import (
	"astrona/internal/localdata"
	"github.com/spf13/cobra"
)

// executeWithDataLock holds a shared lock before command setup until Execute
// returns. Data commands acquire their own exclusive lock only when applying.
// Installing this at the entrypoint keeps command construction side-effect free.
func executeWithDataLock(root *cobra.Command, store func() (localdata.Store, error)) error {
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	var wrap func(*cobra.Command, func(*cobra.Command, []string) error)
	wrap = func(cmd *cobra.Command, inherited func(*cobra.Command, []string) error) {
		if cmd.Name() == "data" {
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
			if release == nil && c.Name() != "help" && c.Name() != "docgen" && c.Name() != "__complete" && c.Name() != "__completeNoDesc" {
				s, err := store()
				if err != nil {
					return err
				}
				release, err = s.Lock(false)
				if err != nil {
					return err
				}
			}
			if before != nil {
				return before(c, args)
			}
			return nil
		}
		for _, child := range cmd.Commands() {
			wrap(child, before)
		}
	}
	wrap(root, nil)
	return root.Execute()
}
