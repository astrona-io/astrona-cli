package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func commandHeadless(cmd *cobra.Command) bool {
	enabled, _ := cmd.Flags().GetBool("headless")
	return enabled
}

// Explicit automation mode wins over future UI defaults, but a contradictory
// explicit UI request is an error before any consent or lab operations.
func configureHeadless(cmd *cobra.Command) error {
	if !commandHeadless(cmd) {
		return nil
	}
	if cmd.Parent() == cmd.Root() {
		switch cmd.Name() {
		case "ui", "shell", "ssh":
			return fmt.Errorf("--headless cannot be combined with astrona %s; this command opens an interactive session", cmd.Name())
		}
	}
	if cmd.Name() != "run" || cmd.Parent() != cmd.Root() {
		return nil
	}
	for _, name := range []string{"ui", "ui-startup"} {
		enabled, err := cmd.Flags().GetBool(name)
		if err != nil {
			return err
		}
		if enabled && cmd.Flags().Changed(name) {
			return fmt.Errorf("--headless cannot be combined with --%s; headless mode runs without the UI", name)
		}
		if err := cmd.Flags().Set(name, "false"); err != nil {
			return err
		}
	}
	return nil
}
