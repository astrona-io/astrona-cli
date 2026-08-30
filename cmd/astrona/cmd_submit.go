package main

import (
	"fmt"

	"astrona/internal/config"
	"astrona/internal/junit"
	"astrona/internal/proctor"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// newSubmitCmd builds `astrona submit`: submit the already-running lab
// environment to the Proctor for grading. This is the "I'm done" entry
// point a trainee runs by hand — grading itself is owned entirely by
// Proctor.Grade, not by this command, so run/destroy/submit never read
// validation.checks/validation.script directly. flags is bound to the root
// command's persistent flags.
func newSubmitCmd(flags *rootFlags) *cobra.Command {
	var junitPath string

	cmd := &cobra.Command{
		Use:          "submit",
		Short:        "Submit the lab to the Proctor for grading",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)

			env, err := runtime.LoadEnvironment(clusterName, cfg.Runtime)
			if err != nil {
				return fmt.Errorf("could not find a running lab environment: %w", err)
			}

			rep, err := ui.NewReporter("submit", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			rep.Section("Proctor")
			pr := proctor.NewProctor(baseDir, env)
			results, pass, err := pr.Grade(cfg)
			if err != nil {
				return err
			}

			if junitPath != "" {
				if err := junit.WriteJUnitReport(junitPath, clusterName, results); err != nil {
					rep.Warn("failed to write JUnit report: %s", err)
				}
			}

			if !pass {
				fmt.Printf("\nPROCTOR: FAIL\n")
				return fmt.Errorf("submission did not pass grading")
			}

			fmt.Printf("\nPROCTOR: PASS\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&junitPath, "junit-xml", "", "Write a JUnit XML test report to this path, for CI systems to parse")

	return cmd
}
