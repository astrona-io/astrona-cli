package main

import (
	"fmt"

	"astrona/internal/addons"
	"astrona/internal/cluster"
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
	var diagMode, diagDir string

	cmd := &cobra.Command{
		Use:          "test",
		Short:        "Run the full lab lifecycle for CI: bootstrap, testing, submit, teardown",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) (retErr error) {
			if err := validateDiagnosticsMode(diagMode); err != nil {
				return err
			}

			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			if err := requireTrust(flags, cfg, baseDir); err != nil {
				return err
			}

			rep, err := ui.NewReporter("test", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			if err := config.ValidateWaitFor(cfg); err != nil {
				return err
			}

			rep.Section("Lab: %s", cfg.Metadata.Name)

			// Forwards are for a human at a browser, not CI — and would
			// clash on host ports with a real `astrona run` of the same lab.
			if err := config.ValidatePortForwards(cfg.Runtime); err != nil {
				return err
			}
			if len(cfg.Runtime.PortForwards) > 0 {
				rep.Info("Skipping %d runtime.portForwards entr(ies) — not started by `astrona test`.", len(cfg.Runtime.PortForwards))
			}

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

			// The test cluster must not compete with a real `run` of the
			// same lab for the gateway's host ports.
			if cfg.Runtime.Kind != nil {
				k := *cfg.Runtime.Kind
				k.Addons.SkipHostPorts = true
				cfg.Runtime.Kind = &k
			}

			env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime, rep)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}

			// Registered before anything else can fail, so every later failure
			// (addons included) still gets diagnostics and a teardown.
			defer func() {
				if wantDiagnostics(diagMode, retErr) {
					rep.Section("Diagnostics")
					collectDiagnostics(env, cfg, clusterName, diagDir, rep)
				}

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

			// Before addons and bootstrap, so anything they start can use
			// the preloaded images.
			if images := labPreloadImages(cfg, baseDir); len(images) > 0 {
				rep.Section("Images")
				if err := cluster.PreloadImages(clusterName, images, rep); err != nil {
					return fmt.Errorf("image preload failed: %w", err)
				}
			}

			if k := cfg.Runtime.Kind; k != nil && !k.Addons.IsZero() {
				rep.Section("Addons")
				if err := addons.Install(k.Addons, env.KubeContext, rep); err != nil {
					return fmt.Errorf("addons failed: %w", err)
				}
			}

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

			if len(cfg.Bootstrap.WaitFor) > 0 {
				rep.Section("Bootstrap readiness")
				if err := manifests.WaitFor(cfg.Bootstrap.WaitFor, env.KubeContext, rep); err != nil {
					return fmt.Errorf("lab did not become ready: %w", err)
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

			// Gate grading on the reference solution actually being up, so
			// the Proctor doesn't race pods that are still starting.
			if len(cfg.Testing.WaitFor) > 0 {
				rep.Section("Testing readiness")
				if err := manifests.WaitFor(cfg.Testing.WaitFor, env.KubeContext, rep); err != nil {
					return fmt.Errorf("reference solution did not become ready: %w", err)
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
	cmd.Flags().StringVar(&diagMode, "diagnostics", diagnosticsOnFailure, "When to collect a diagnostics bundle before teardown: on-failure, always, or never")
	cmd.Flags().StringVar(&diagDir, "diagnostics-dir", "", "Write the diagnostics bundle here (default ~/.astrona/diagnostics/<lab>-<timestamp>) — point it inside your CI workspace to upload it as an artifact")

	return cmd
}
