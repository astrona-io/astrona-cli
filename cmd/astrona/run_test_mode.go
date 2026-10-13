package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func runTestMode(cmd *cobra.Command) bool {
	enabled, _ := cmd.Flags().GetBool("test-mode")
	return cmd.Name() == "run" && enabled
}

// Explicit automation mode wins over future UI defaults, but a contradictory
// explicit UI request is an error before any consent or lab operations.
func configureRunTestMode(cmd *cobra.Command) error {
	if !runTestMode(cmd) {
		return nil
	}
	for _, name := range []string{"ui", "ui-startup"} {
		enabled, err := cmd.Flags().GetBool(name)
		if err != nil {
			return err
		}
		if enabled && cmd.Flags().Changed(name) {
			return fmt.Errorf("--test-mode cannot be combined with --%s; test mode runs without the UI", name)
		}
		if err := cmd.Flags().Set(name, "false"); err != nil {
			return err
		}
	}
	return nil
}
