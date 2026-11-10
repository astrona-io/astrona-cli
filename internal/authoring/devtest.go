package authoring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/manifests"
	"astrona/internal/proctor"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"
)

// ErrTestInterrupted is what RunTest returns when its context was
// cancelled (Ctrl-C, SIGTERM) — after its deferred diagnostics and
// teardown have run.
var ErrTestInterrupted = errors.New("astrona test was interrupted by a signal")

// ErrReferenceFailed is a TestRun's error when the reference solution was
// graded and did not pass — a grading result, not a failure of astrona or
// the setup. The caller maps it to its "not passed" exit code.
var ErrReferenceFailed = errors.New("reference solution did not pass grading")

// TestOptions configure one RunTest lifecycle.
type TestOptions struct {
	// ClusterName is the test environment's name
	// (config.NormalizeTestClusterName), distinct from a real run's.
	ClusterName string
	// Parallel bounds how many linked clusters start at once.
	Parallel int
	// Out receives the Proctor's report, validation scripts' output and
	// the PROCTOR verdict line.
	Out io.Writer
	// Diagnose, when set, runs before teardown once the environment
	// exists, with the run's error (nil when it passed). It decides
	// whether to collect a bundle.
	Diagnose func(env *runtime.LabEnvironment, runErr error)
}

// TestRun is one RunTest lifecycle's outcome. Results is nil when the run
// failed before grading (setup, bootstrap, readiness); Err is
// ErrReferenceFailed when it was graded and did not pass.
type TestRun struct {
	Results []proctor.CheckResult
	Pass    bool
	Err     error
}

// RunTest is one full `astrona test` lifecycle on a fresh environment:
// clean slate, create, preload, addons, bootstrap, testing, grade, and —
// always, via defer — diagnostics (opts.Diagnose) and teardown. A
// cancelled ctx (Ctrl-C, SIGTERM) stops it at the next step; the defers
// still run. Progress goes to rep; the grading report to opts.Out.
func RunTest(ctx context.Context, cfg *config.LabConfig, baseDir string, opts TestOptions, rep *ui.Reporter) TestRun {
	results, pass, err := runTest(ctx, cfg, baseDir, opts, rep)
	return TestRun{Results: results, Pass: pass, Err: err}
}

func runTest(ctx context.Context, cfg *config.LabConfig, baseDir string, opts TestOptions, rep *ui.Reporter) (results []proctor.CheckResult, pass bool, retErr error) {
	clusterName := opts.ClusterName
	release, err := labstate.LockMutation(clusterName)
	if err != nil {
		return nil, false, err
	}
	defer release()
	previousLinks, err := cluster.ReadLinks(clusterName)
	if err != nil {
		return nil, false, fmt.Errorf("read test lab's linked ownership: %w", err)
	}
	// Best-effort clean slate: Ctrl-C/SIGTERM are caught by the caller
	// so the deferred teardown below runs, but a SIGKILL or a crash
	// still skips it and can leave clusterName's environment behind.
	// DestroyEnvironment is already a documented no-op when nothing
	// exists (DestroyQEMUVM/DeleteKindCluster both tolerate a
	// missing target), so this makes every `astrona test` start
	// fresh without needing to first detect whether a leftover
	// actually exists.
	if err := runtime.DestroyEnvironment(clusterName, cfg.Runtime, rep); err != nil {
		rep.Warn("could not clean up a previous '%s' test environment, proceeding anyway: %s", clusterName, err)
	}
	// Deleting the parent clears its runtime directory; retain evidence for
	// linked clusters left behind by an interrupted earlier test.
	if len(previousLinks) > 0 {
		if err := cluster.WriteLinks(clusterName, previousLinks); err != nil {
			return nil, false, err
		}
	}
	if ctx.Err() != nil {
		return nil, false, ErrTestInterrupted
	}

	// Linked clusters come up first; their teardown is deferred before this
	// lab's, so it runs after it (LIFO) — even on failure.
	var owned []string
	defer func() {
		if !cfg.Teardown.KeepCluster { // kept with the lab, like its own cluster
			lifecycle.DestroyOwnedClusters(clusterName, owned, rep)
		}
	}()
	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil {
		return nil, false, err
	}
	links, err := lifecycle.StartLinkedClusters(ctx, cfg, baseDir, clusterName, true, opts.Parallel, rep)
	// The main cluster's teardown removes links.json, so retain ownership
	// before its deferred cleanup runs, including a partial linked setup.
	owned = lifecycle.OwnedClusters(clusterName, nil)
	if err != nil {
		return nil, false, err
	}
	if ctx.Err() != nil {
		return nil, false, ErrTestInterrupted
	}

	// The same pipeline as `astrona run`, as a test copy (no host ports,
	// no port forwards).
	env, _, upErr := lifecycle.Up(ctx, cfg, baseDir, clusterName, links, true, rep)
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
		if opts.Diagnose != nil {
			opts.Diagnose(env, retErr)
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
		return nil, false, ErrTestInterrupted
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
			return nil, false, ErrTestInterrupted
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
		return nil, false, ErrTestInterrupted
	}
	if err := applyTesting(cfg.Testing, baseDir, env, cfg.Runtime.QEMU, "Testing", rep); err != nil {
		return nil, false, err
	}
	if ctx.Err() != nil {
		return nil, false, ErrTestInterrupted
	}

	// Grading writes its own pytest-style report to opts.Out — pause the
	// reporter's log-only section header and let it through.
	rep.Section("Proctor")
	pr := proctor.NewProctor(baseDir, env)
	pr.ScriptOutputTo(opts.Out)
	start := time.Now()
	results, pass, err = pr.Evaluate(cfg)
	if err != nil {
		return nil, false, err
	}
	pr.Report(opts.Out, cfg, results, time.Since(start))
	fmt.Fprintf(opts.Out, "\nPROCTOR: %s\n", ui.PassFail(opts.Out, pass, 0))
	if !pass {
		return results, false, ErrReferenceFailed
	}
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
