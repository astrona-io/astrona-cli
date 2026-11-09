package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func commandHeadless(cmd *cobra.Command) bool {
	enabled, _ := cmd.Flags().GetBool("headless")
	return enabled
}

// Explicit automation mode refuses the commands that only open an
// interactive session, before any consent or lab operations.
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
	return nil
}

// installHeadlessGate checks --headless against interactive-only commands
// on every command, before its own RunE.
func installHeadlessGate(root *cobra.Command) {
	installCommandGate(root, configureHeadless)
}
