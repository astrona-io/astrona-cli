package main

import (
	"fmt"
	"os"
	"path/filepath"

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
	var repeat int

	cmd := &cobra.Command{
		Use:          "test [lab]",
		Short:        "Run the full lab lifecycle for CI: bootstrap, testing, submit, teardown",
		SilenceUsage: true,
		Long: "Run the full lab lifecycle for CI: bootstrap, apply the reference solution (testing), " +
			"grade it, and always tear down — proving the lab's own solution passes its checks.\n\n" +
			"--repeat N runs the whole lifecycle N times on fresh environments and reports any check " +
			"that doesn't pass every time (flaky), so race-prone checks are caught before students " +
			"hit them.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			useLabArg(args, flags) // a lab given as the argument wins over `astrona use`
			if err := validateDiagnosticsMode(diagMode); err != nil {
				return err
			}
			if err := validateParallel(flags.parallel); err != nil {
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

			if repeat < 1 || repeat > maxTestRepeat {
				return fmt.Errorf("--repeat must be between 1 and %d", maxTestRepeat)
			}

			var runs []testRun
			for i := 1; i <= repeat; i++ {
				if repeat > 1 {
					rep.Section("Run %d/%d", i, repeat)
				}
				dir := diagDir
				if dir != "" && repeat > 1 {
					dir = filepath.Join(diagDir, fmt.Sprintf("run-%d", i))
				}
				results, pass, err := runTestOnce(cfg, baseDir, clusterName, diagMode, dir, flags, rep)
				runs = append(runs, testRun{results: results, pass: pass, err: err})
			}

			final := runs[len(runs)-1].results
			if repeat > 1 {
				final = aggregateRuns(runs)
			}
			if junitPath != "" && final != nil {
				if err := junit.WriteJUnitReport(junitPath, clusterName, final); err != nil {
					rep.Warn("failed to write JUnit report: %s", err)
				}
			}

			if repeat == 1 {
				return runs[0].err
			}
			rep.Close()
			return printRepeatSummary(os.Stdout, runs)
		},
	}

	cmd.Flags().StringVar(&junitPath, "junit-xml", "", "Write a JUnit XML test report to this path, for CI systems to parse")
	cmd.Flags().StringVar(&diagMode, "diagnostics", diagnosticsOnFailure, "When to collect a diagnostics bundle before teardown: on-failure, always, or never")
	addParallelFlag(cmd, flags)
	cmd.Flags().IntVar(&repeat, "repeat", 1, "Run the whole lifecycle this many times (fresh environment each) and report flaky checks")
	cmd.Flags().StringVar(&diagDir, "diagnostics-dir", "", "Write the diagnostics bundle here (default ~/.astrona/diagnostics/<lab>-<timestamp>) — point it inside your CI workspace to upload it as an artifact")

	return cmd
}

// runTestOnce is one full `astrona test` lifecycle on a fresh environment:
// clean slate, create, preload, addons, bootstrap, testing, grade, and —
// always, via defer — diagnostics (per diagMode) and teardown.
func runTestOnce(cfg *config.LabConfig, baseDir, clusterName, diagMode, diagDir string, flags *rootFlags, rep *ui.Reporter) (results []proctor.CheckResult, pass bool, retErr error) {
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

	// Linked clusters come up first; their teardown is deferred before this
	// lab's, so it runs after it (LIFO) — even on failure.
	defer func() {
		if !cfg.Teardown.KeepCluster { // kept with the lab, like its own cluster
			destroyOwnedClusters(ownedClusters(clusterName, cfg.KindClusters()), rep)
		}
	}()
	if err := prepareSharedCA(cfg, clusterName); err != nil {
		return nil, false, err
	}
	links, err := startKindClusters(cfg, baseDir, clusterName, true, flags.parallel, rep)
	if err != nil {
		return nil, false, err
	}

	env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime, rep)
	if err != nil {
		return nil, false, fmt.Errorf("lab setup failed: %w", err)
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
		runLinkedTeardown(cfg.KindClusters(), clusterName, baseDir, rep)

		if cfg.Teardown.KeepCluster {
			rep.Info("keepCluster is set, leaving cluster '%s' running.", clusterName)
			return
		}

		if err := runtime.DestroyEnvironment(clusterName, cfg.Runtime, rep); err != nil {
			rep.Warn("cluster delete failed: %s", err)
		}
	}()

	if err := attachLinks(env, clusterName, links, rep); err != nil {
		return nil, false, err
	}
	refreshLinkNames(clusterName, true, rep)

	// Before addons and bootstrap, so anything they start can use
	// the preloaded images.
	if k := cfg.Runtime.Kind; k != nil && len(k.PreloadImages) > 0 {
		rep.Section("Images")
		if err := cluster.PreloadImages(clusterName, k.PreloadImages, rep); err != nil {
			return nil, false, fmt.Errorf("image preload failed: %w", err)
		}
	}

	if k := cfg.Runtime.Kind; k != nil && !k.Addons.IsZero() {
		rep.Section("Addons")
		if err := addons.Install(k.Addons, env.KubeContext, rep); err != nil {
			return nil, false, fmt.Errorf("addons failed: %w", err)
		}
	}

	if err := installSharedCA(cfg, env, rep); err != nil {
		return nil, false, err
	}

	if err := waitForClusterDNS(cfg, env, rep); err != nil {
		return nil, false, fmt.Errorf("cluster DNS not ready: %w", err)
	}

	if scripts.HasBootstrapInit(cfg) {
		rep.Section("Bootstrap")
		if err := scripts.RunBootstrap(cfg, baseDir, env, rep); err != nil {
			return nil, false, fmt.Errorf("bootstrap init scripts failed: %w", err)
		}
	}

	if len(cfg.Bootstrap.Manifests) > 0 {
		if env.KubeContext == "" {
			return nil, false, fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
		}
		rep.Section("Bootstrap manifests")
		if err := manifests.ApplyManifests(cfg.Bootstrap.Manifests, baseDir, env.KubeContext, rep); err != nil {
			return nil, false, fmt.Errorf("bootstrap manifests failed: %w", err)
		}
	}

	if len(cfg.Bootstrap.WaitFor) > 0 {
		rep.Section("Bootstrap readiness")
		if err := manifests.WaitFor(cfg.Bootstrap.WaitFor, env.KubeContext, rep); err != nil {
			return nil, false, fmt.Errorf("lab did not become ready: %w", err)
		}
	}

	// Each linked cluster's part of the reference solution first, in
	// dependency order, then the lab's own — which may rely on them.
	order, err := config.KindClusterOrder(cfg.KindClusters())
	if err != nil {
		return nil, false, err
	}
	for _, l := range order {
		if isEmptyBlock(l.Testing) {
			continue
		}
		name := config.LinkedClusterName(clusterName, l.Name)
		linkEnv, err := runtime.LoadEnvironment(name, kindClusterConfig(cfg, l).Runtime)
		if err != nil {
			return nil, false, fmt.Errorf("linked cluster '%s': %w", l.Name, err)
		}
		if err := applyTesting(l.Testing, baseDir, linkEnv, nil, "Testing: cluster "+l.Name, rep); err != nil {
			return nil, false, fmt.Errorf("linked cluster '%s': %w", l.Name, err)
		}
	}
	if err := applyTesting(cfg.Testing, baseDir, env, cfg.Runtime.QEMU, "Testing", rep); err != nil {
		return nil, false, err
	}

	// Grading prints its own pytest-style report to stdout — pause
	// the reporter's log-only section header and let it through.
	rep.Section("Proctor")
	pr := proctor.NewProctor(baseDir, env)
	results, pass, err = pr.Grade(cfg)
	if err != nil {
		return nil, false, err
	}
	if !pass {
		fmt.Printf("\nPROCTOR: FAIL\n")
		return results, false, fmt.Errorf("reference solution did not pass grading")
	}
	fmt.Printf("\nPROCTOR: PASS\n")
	return results, true, nil
}

func isEmptyBlock(b config.BootstrapConfig) bool {
	return len(b.Init) == 0 && len(b.Manifests) == 0 && len(b.WaitFor) == 0
}

// applyTesting applies a testing block (the reference solution) to env:
// init scripts, manifests, then readiness gates — so the Proctor doesn't
// race pods that are still starting. title heads its output sections.
func applyTesting(b config.BootstrapConfig, baseDir string, env *runtime.LabEnvironment, vms []config.QEMUVM, title string, rep *ui.Reporter) error {
	if len(b.Init) > 0 {
		rep.Section("%s", title)
		if err := scripts.RunOnEveryVM(b.Init, baseDir, env, vms, rep); err != nil {
			return fmt.Errorf("testing init scripts failed: %w", err)
		}
	}
	if len(b.Manifests) > 0 {
		if env.KubeContext == "" {
			return fmt.Errorf("testing.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
		}
		rep.Section("%s manifests", title)
		if err := manifests.ApplyManifests(b.Manifests, baseDir, env.KubeContext, rep); err != nil {
			return fmt.Errorf("testing manifests failed: %w", err)
		}
	}
	if len(b.WaitFor) > 0 {
		rep.Section("%s readiness", title)
		if err := manifests.WaitFor(b.WaitFor, env.KubeContext, rep); err != nil {
			return fmt.Errorf("reference solution did not become ready: %w", err)
		}
	}
	return nil
}
