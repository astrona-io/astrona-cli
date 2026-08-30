package main

import (
	"fmt"

	"astrona/internal/config"
	"astrona/internal/junit"
	"astrona/internal/manifests"
	"astrona/internal/proctor"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"

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
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			rep, err := ui.NewReporter("test", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			rep.Section("Lab: %s", cfg.Metadata.Name)

			// Prefixed so a CI/dev `test` run never collides with a real
			// `astrona run` environment for the same lab config running at
			// the same time (same kind cluster name / same qemu state dir
			// otherwise).
			clusterName := config.NormalizeTestClusterName(cfg.Metadata.Name)

			// Best-effort clean slate: a cancelled `astrona test` (Ctrl-C)
			// skips the defer teardown below entirely — Go doesn't run
			// deferred functions on a signal that kills the process — so a
			// crashed run can leave clusterName's environment behind.
			// DestroyEnvironment is already a documented no-op when nothing
			// exists (DestroyQEMUVM/DeleteKindCluster both tolerate a
			// missing target), so this makes every `astrona test` start
			// fresh without needing to first detect whether a leftover
			// actually exists.
			if err := runtime.DestroyEnvironment(clusterName, cfg.Runtime, rep); err != nil {
				rep.Warn("could not clean up a previous '%s' test environment, proceeding anyway: %s", clusterName, err)
			}

			env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime, rep)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}

			defer func() {
				if len(cfg.Teardown.Init) > 0 {
					rep.Section("Teardown")
					if err := scripts.RunOnEveryVM(cfg.Teardown.Init, baseDir, env, cfg.Runtime.QEMU, rep); err != nil {
						rep.Warn("teardown scripts failed: %s", err)
					}
				}

				if cfg.Teardown.KeepCluster {
					rep.Info("keepCluster is set, leaving cluster '%s' running.", clusterName)
					return
				}

				if err := runtime.DestroyEnvironment(clusterName, cfg.Runtime, rep); err != nil {
					rep.Warn("cluster delete failed: %s", err)
				}
			}()

			if scripts.HasBootstrapInit(cfg) {
				rep.Section("Bootstrap")
				if err := scripts.RunBootstrap(cfg, baseDir, env, rep); err != nil {
					return fmt.Errorf("bootstrap init scripts failed: %w", err)
				}
			}

			if len(cfg.Bootstrap.Manifests) > 0 {
				if env.KubeContext == "" {
					return fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
				}
				rep.Section("Bootstrap manifests")
				if err := manifests.ApplyManifests(cfg.Bootstrap.Manifests, baseDir, env.KubeContext, rep); err != nil {
					return fmt.Errorf("bootstrap manifests failed: %w", err)
				}
			}

			if len(cfg.Testing.Init) > 0 {
				rep.Section("Testing")
				if err := scripts.RunOnEveryVM(cfg.Testing.Init, baseDir, env, cfg.Runtime.QEMU, rep); err != nil {
					return fmt.Errorf("testing init scripts failed: %w", err)
				}
			}

			if len(cfg.Testing.Manifests) > 0 {
				if env.KubeContext == "" {
					return fmt.Errorf("testing.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
				}
				rep.Section("Testing manifests")
				if err := manifests.ApplyManifests(cfg.Testing.Manifests, baseDir, env.KubeContext, rep); err != nil {
					return fmt.Errorf("testing manifests failed: %w", err)
				}
			}

			// Grading prints its own pytest-style report to stdout — pause
			// the reporter's log-only section header and let it through.
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
				return fmt.Errorf("reference solution did not pass grading")
			}

			fmt.Printf("\nPROCTOR: PASS\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&junitPath, "junit-xml", "", "Write a JUnit XML test report to this path, for CI systems to parse")

	return cmd
}
