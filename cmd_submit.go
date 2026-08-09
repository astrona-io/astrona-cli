package main

import (
	"fmt"

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
			config, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			clusterName := config.Metadata.Name
			if clusterName == "" {
				clusterName = "astrona-lab"
			}

			env, err := LoadEnvironment(clusterName, config.Runtime)
			if err != nil {
				return fmt.Errorf("could not find a running lab environment: %w", err)
			}

			fmt.Printf("Submitting to the Proctor...\n")
			proctor := NewProctor(baseDir, env.KubeContext, env.Executor)
			results, pass, err := proctor.Grade(config)
			if err != nil {
				return err
			}

			if junitPath != "" {
				if err := WriteJUnitReport(junitPath, clusterName, results); err != nil {
					fmt.Printf("[WARN] failed to write JUnit report: %s\n", err)
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
