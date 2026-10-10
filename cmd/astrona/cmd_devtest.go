package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"astrona/internal/config"
	"astrona/internal/junit"
	"astrona/internal/lifecycle"
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
			"Ctrl-C or SIGTERM (a cancelled CI job) stops the run after the current step, then " +
			"still collects diagnostics (per --diagnostics) and tears the environment down. Steps " +
			"astrona is waiting on are not cut short by SIGTERM; send SIGKILL to quit without " +
			"tearing down.\n\n" +
			"--repeat N runs the whole lifecycle N times on fresh environments and reports any check " +
			"that doesn't pass every time (flaky), so race-prone checks are caught before students " +
			"hit them.\n\n" +
			"Exit code: 0 passed, 2 graded but didn't pass, 1 something else went wrong (setup, " +
			"tools, config) — see the CI guide.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := labArg(args, flags); err != nil { // a lab given as the argument wins over `astrona use`
				return err
			}
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

			// Up front, before anything is created: a broken config is an
			// error (exit 1), not a failed grade (exit 2).
			if err := lifecycle.Validate(cfg); err != nil {
				return err
			}

			rep.Section("Lab: %s", cfg.Metadata.Name)

			// Forwards are for a human at a browser, not CI — and would
			// clash on host ports with a real `astrona run` of the same lab.
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

			// Catch Ctrl-C / SIGTERM instead of dying on them: a killed
			// process skips every deferred diagnostics and teardown below
			// and leaves the test cluster behind. Each run stops at its
			// next step and unwinds normally instead.
			ctx, stopSignals := cancelOnSignal(func(sig os.Signal, first bool) {
				if first {
					ui.Warnf("received %s — stopping after the current step, then tearing down (SIGKILL quits without tearing down)", sig)
					return
				}
				ui.Warnf("received %s — already stopping, teardown is still running", sig)
			}, os.Interrupt, syscall.SIGTERM)
			defer stopSignals()

			var runs []testRun
			for i := 1; i <= repeat && ctx.Err() == nil; i++ {
				if repeat > 1 {
					rep.Section("Run %d/%d", i, repeat)
				}
				dir := diagDir
				if dir != "" && repeat > 1 {
					dir = filepath.Join(diagDir, fmt.Sprintf("run-%d", i))
				}
				results, pass, err := runTestOnce(ctx, cfg, baseDir, clusterName, diagMode, dir, flags, rep)
				runs = append(runs, testRun{results: results, pass: pass, err: err})
			}
			if ctx.Err() != nil {
				return errTestInterrupted
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

// errTestInterrupted is what `astrona test` returns when a signal stopped
// it — after its deferred diagnostics and teardown have run.
var errTestInterrupted = errors.New("astrona test was interrupted by a signal")

// cancelOnSignal returns a context cancelled by the first of sigs. While
// it's active those signals no longer kill the process — a later one only
// calls onSignal again (first=false) — so a cancelled CI job, which often
// sends SIGINT then SIGTERM, can't cut the teardown short. stop restores
// the default handling.
func cancelOnSignal(onSignal func(sig os.Signal, first bool), sigs ...os.Signal) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-ch:
				first := ctx.Err() == nil
				cancel()
				if onSignal != nil {
					onSignal(sig, first)
				}
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
			cancel()
		})
	}
}

// runTestOnce is one full `astrona test` lifecycle on a fresh environment:
// clean slate, create, preload, addons, bootstrap, testing, grade, and —
// always, via defer — diagnostics (per diagMode) and teardown. A cancelled
// ctx (Ctrl-C, SIGTERM) stops it at the next step; the defers still run.
func runTestOnce(ctx context.Context, cfg *config.LabConfig, baseDir, clusterName, diagMode, diagDir string, flags *rootFlags, rep *ui.Reporter) (results []proctor.CheckResult, pass bool, retErr error) {
	// Best-effort clean slate: Ctrl-C/SIGTERM are caught (see
	// cancelOnSignal) so the deferred teardown below runs, but a SIGKILL
	// or a crash still skips it and can leave clusterName's environment
	// behind.
	// DestroyEnvironment is already a documented no-op when nothing
	// exists (DestroyQEMUVM/DeleteKindCluster both tolerate a
	// missing target), so this makes every `astrona test` start
	// fresh without needing to first detect whether a leftover
	// actually exists.
	if err := runtime.DestroyEnvironment(clusterName, cfg.Runtime, rep); err != nil {
		rep.Warn("could not clean up a previous '%s' test environment, proceeding anyway: %s", clusterName, err)
	}
	if ctx.Err() != nil {
		return nil, false, errTestInterrupted
	}

	// Linked clusters come up first; their teardown is deferred before this
	// lab's, so it runs after it (LIFO) — even on failure.
	defer func() {
		if !cfg.Teardown.KeepCluster { // kept with the lab, like its own cluster
			lifecycle.DestroyOwnedClusters(lifecycle.OwnedClusters(clusterName, cfg.KindClusters()), rep)
		}
	}()
	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil {
		return nil, false, err
	}
	links, err := lifecycle.StartLinkedClusters(cfg, baseDir, clusterName, true, flags.parallel, rep)
	if err != nil {
		return nil, false, err
	}
	if ctx.Err() != nil {
		return nil, false, errTestInterrupted
	}

	// The same pipeline as `astrona run`, as a test copy (no host ports,
	// no port forwards).
	env, _, upErr := lifecycle.Up(cfg, baseDir, clusterName, links, true, rep)
	if env == nil {
		// Creation failed partway — kind may already have made the cluster.
		if err := runtime.DestroyEnvironment(clusterName, cfg.Runtime, rep); err != nil {
			rep.Warn("could not clean up '%s': %s", clusterName, err)
		}
		return nil, false, upErr
	}
	// Registered as soon as the environment exists, so every later failure
	// (addons, bootstrap, …) still gets diagnostics and a teardown.
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
		lifecycle.RunLinkedTeardown(cfg.KindClusters(), clusterName, baseDir, rep)

		if cfg.Teardown.KeepCluster {
			rep.Info("keepCluster is set, leaving cluster '%s' running.", clusterName)
			return
		}

		if err := runtime.DestroyEnvironment(clusterName, cfg.Runtime, rep); err != nil {
			rep.Warn("cluster delete failed: %s", err)
		}
	}()
	if upErr != nil {
		return nil, false, upErr
	}
	if ctx.Err() != nil {
		return nil, false, errTestInterrupted
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
		if ctx.Err() != nil {
			return nil, false, errTestInterrupted
		}
		name := config.LinkedClusterName(clusterName, l.Name)
		linkEnv, err := runtime.LoadEnvironment(name, lifecycle.LinkedClusterConfig(cfg, l).Runtime)
		if err != nil {
			return nil, false, fmt.Errorf("linked cluster '%s': %w", l.Name, err)
		}
		if err := applyTesting(l.Testing, baseDir, linkEnv, nil, "Testing: cluster "+l.Name, rep); err != nil {
			return nil, false, fmt.Errorf("linked cluster '%s': %w", l.Name, err)
		}
	}
	if ctx.Err() != nil {
		return nil, false, errTestInterrupted
	}
	if err := applyTesting(cfg.Testing, baseDir, env, cfg.Runtime.QEMU, "Testing", rep); err != nil {
		return nil, false, err
	}
	if ctx.Err() != nil {
		return nil, false, errTestInterrupted
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
		fmt.Printf("\nPROCTOR: %s\n", ui.PassFail(os.Stdout, false, 0))
		return results, false, notPassed("reference solution did not pass grading")
	}
	fmt.Printf("\nPROCTOR: %s\n", ui.PassFail(os.Stdout, true, 0))
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
