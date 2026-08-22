package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// newDocgenCmd generates a markdown page per command into --output, using
// Cobra's own doc generator against the exact command tree newRootCmd builds
// — so docs/reference/cli/ can never drift from the real Use/Short/Long text
// and flags. Hidden: it's a docs-build-time tool for `just build-docs`, not
// something a lab operator ever needs to run.
func newDocgenCmd(flags *rootFlags) *cobra.Command {
	var outputDir string

	cmd := &cobra.Command{
		Use:    "docgen",
		Short:  "Generate CLI reference markdown (docs build tooling, not for end users)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A fresh tree built by newRootCmd never has docgen itself
			// attached, so the generated reference only covers the CLI's
			// real public surface.
			root := newRootCmd(flags)
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create CLI reference output dir %s: %w", outputDir, err)
			}
			if err := doc.GenMarkdownTree(root, outputDir); err != nil {
				return fmt.Errorf("generate CLI reference markdown: %w", err)
			}
			fmt.Printf("CLI reference written to %s\n", outputDir)
			return nil
		},
	}

	cmd.Flags().StringVar(&outputDir, "output", "docs/reference/cli", "Output directory for generated CLI reference markdown")

	return cmd
}
