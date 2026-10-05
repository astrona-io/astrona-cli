package main

import (
	"os"

	"astrona/internal/config"

	"github.com/spf13/cobra"
)

// newSchemaCmd prints the lab config JSON Schema generated from the
// config structs. Hidden: it regenerates docs/schema/lab-config.schema.json
// (`astrona schema > docs/schema/lab-config.schema.json`), which the docs
// site publishes at config.SchemaURL; TestSchemaFileUpToDate keeps the two
// in sync.
func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "schema",
		Short:  "Print the lab config JSON Schema (docs build tooling)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := config.Schema()
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(out)
			return err
		},
	}
}
