package main

import (
	"fmt"
	"path/filepath"

	"astrona/internal/authoring"
	"astrona/internal/config"
	"astrona/internal/preflight"

	"github.com/spf13/cobra"
)

// labConfigResults checks a loaded lab config without creating anything
// (authoring.CheckConfig). It is the preflight.ConfigChecker that check,
// doctor and run pass to the lab checks.
var labConfigResults preflight.ConfigChecker = authoring.CheckConfig

func newValidateCmd(flags *rootFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "validate [lab]",
		Short: "Check a lab config for mistakes without running anything",
		Long: "Check a lab config (-c/--file/--git) without creating anything: unknown fields (typos " +
			"like `waitfor` that would otherwise be silently ignored, with a did-you-mean), runtime/" +
			"addon/port-forward/waitFor rules, validation check types, and that every local " +
			"script, manifest and doc file it references exists.\n\n" +
			"Exits non-zero on any ✗ — suitable as a CI step for lab repositories. For editor " +
			"autocompletion, see the JSON Schema in the lab config reference.",
		Example: `  astrona validate -c ./labs/my-lab
  astrona validate --git https://github.com/org/labs --config labs/lab-01`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := labArg(args, flags); err != nil { // a lab given as the argument wins over `astrona use`
				return err
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			rep := &report{json: output == "json"}
			finalPath, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, flags.verbose)
			if err != nil {
				return rep.loadFailed(flags.configPath, withNoLabHint(err, flags))
			}
			cfg, cleanup, err := config.LoadLabConfig(finalPath)
			if err != nil {
				if want := labVersionFromLoadError(err); want != "" {
					if verr := ensureLabVersion(want, flags, handoverApproval(flags, nil, err, filepath.Dir(finalPath))); verr != nil {
						return rep.loadFailed(finalPath, verr)
					}
				}
				return rep.loadFailed(finalPath, withNoLabHint(err, flags))
			}
			if err := ensureLabVersion(cfg.AstronaVersion, flags, handoverApproval(flags, cfg, nil, filepath.Dir(finalPath))); err != nil {
				cleanup()
				return rep.loadFailed(finalPath, err)
			}
			defer cleanup()

			vr := authoring.ValidateConfig(cfg, finalPath)
			failed := rep.section(vr.Path, vr.Results)
			if rep.json {
				return rep.done(fmt.Errorf("%d problem(s) in the lab config", failed))
			}
			if failed > 0 {
				return fmt.Errorf("%d problem(s) in the lab config", failed)
			}
			fmt.Println("\nLab config is valid.")
			return nil
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}
