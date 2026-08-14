package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newTestCmd builds `astrona test`: the full CI pipeline in one command —
// bootstrap, apply the reference "testing" solution, submit to the Proctor,
// then always tear down (even on failure) so CI never leaks a cluster. This
// is a lab-developer/CI concern (proving a lab's reference solution
// actually passes the Proctor's own checks), not something a student runs
// while taking the lab. flags is bound to the root command's persistent
// flags. Lives in cmd_devtest.go, not cmd_test.go — a file ending in
// _test.go is treated as a Go test file and silently excluded from the
// build.
func newTestCmd(flags *rootFlags) *cobra.Command {
	var junitPath string

	cmd := &cobra.Command{
		Use:          "test",
		Short:        "Run the full lab lifecycle for CI: bootstrap, testing, submit, teardown",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			config, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			// Prefixed so a CI/dev `test` run never collides with a real
			// `astrona run` environment for the same lab config running at
			// the same time (same kind cluster name / same qemu state dir
			// otherwise).
			clusterName := normalizeTestClusterName(config.Metadata.Name)

			// Best-effort clean slate: a cancelled `astrona test` (Ctrl-C)
			// skips the defer teardown below entirely — Go doesn't run
			// deferred functions on a signal that kills the process — so a
			// crashed run can leave clusterName's environment behind.
			// DestroyEnvironment is already a documented no-op when nothing
			// exists (DestroyQEMUVM/DeleteKindCluster both tolerate a
			// missing target), so this makes every `astrona test` start
			// fresh without needing to first detect whether a leftover
			// actually exists.
			if err := DestroyEnvironment(clusterName, config.Runtime); err != nil {
				fmt.Printf("[WARN] could not clean up a previous '%s' test environment, proceeding anyway: %s\n", clusterName, err)
			}

			env, err := CreateEnvironment(clusterName, baseDir, config.Runtime)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}

			defer func() {
				if len(config.Teardown.Init) > 0 {
					fmt.Printf("Running teardown scripts...\n")
					if err := runOnEveryVM(config.Teardown.Init, baseDir, env, config.Runtime.QEMU); err != nil {
						fmt.Printf("[WARN] teardown scripts failed: %s\n", err)
					}
				}

				if config.Teardown.KeepCluster {
					fmt.Printf("keepCluster is set, leaving cluster '%s' running.\n", clusterName)
					return
				}

				if err := DestroyEnvironment(clusterName, config.Runtime); err != nil {
					fmt.Printf("[WARN] cluster delete failed: %s\n", err)
				}
			}()

			if len(config.Bootstrap.Init) > 0 {
				fmt.Printf("Running bootstrap init scripts...\n")
				if err := runBootstrap(config, baseDir, env); err != nil {
					return fmt.Errorf("bootstrap init scripts failed: %w", err)
				}
			}

			if len(config.Bootstrap.Manifests) > 0 {
				if env.KubeContext == "" {
					return fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
				}
				fmt.Printf("Applying bootstrap manifests...\n")
				if err := ApplyManifests(config.Bootstrap.Manifests, baseDir, env.KubeContext); err != nil {
					return fmt.Errorf("bootstrap manifests failed: %w", err)
				}
			}

			if len(config.Testing.Init) > 0 {
				fmt.Printf("Running testing init scripts...\n")
				if err := runOnEveryVM(config.Testing.Init, baseDir, env, config.Runtime.QEMU); err != nil {
					return fmt.Errorf("testing init scripts failed: %w", err)
				}
			}

			if len(config.Testing.Manifests) > 0 {
				if env.KubeContext == "" {
					return fmt.Errorf("testing.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
				}
				fmt.Printf("Applying testing manifests...\n")
				if err := ApplyManifests(config.Testing.Manifests, baseDir, env.KubeContext); err != nil {
					return fmt.Errorf("testing manifests failed: %w", err)
				}
			}

			fmt.Printf("Submitting to the Proctor...\n")
			proctor := NewProctor(baseDir, env)
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
				return fmt.Errorf("reference solution did not pass grading")
			}

			fmt.Printf("\nPROCTOR: PASS\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&junitPath, "junit-xml", "", "Write a JUnit XML test report to this path, for CI systems to parse")

	return cmd
}
