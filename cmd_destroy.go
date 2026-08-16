package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// teardownInfo is what `astrona destroy` needs from the lab config — best
// effort, since destroy must still run even if the config is missing or
// unreadable (e.g. the lab directory was already cleaned up).
type teardownInfo struct {
	clusterName string
	teardown    TeardownConfig
	runtime     RuntimeConfig
}

// loadTeardownInfo tries to load finalPath for the extra info destroy can
// use (cluster name, teardown scripts, runtime type). It returns the load
// error rather than swallowing it — a missing/unreadable config means
// destroy can't trust the fallback "astrona-lab" name (nothing may actually
// be running under that name), so the caller must fall back to discovery
// (destroyByDiscovery) instead of silently "destroying" a name that was
// never real. See newDestroyCmd.
func loadTeardownInfo(finalPath string) (teardownInfo, func(), error) {
	info := teardownInfo{clusterName: "astrona-lab"}
	cleanup := func() {}

	if finalPath == "" {
		return info, cleanup, fmt.Errorf("no config path resolved")
	}

	config, configCleanup, err := LoadLabConfig(finalPath)
	if err != nil {
		return info, cleanup, err
	}
	cleanup = configCleanup

	if config.Metadata.Name != "" {
		info.clusterName = config.Metadata.Name
	}
	info.teardown = config.Teardown
	info.runtime = config.Runtime

	return info, cleanup, nil
}

// destroyByDiscovery is the fallback path for `astrona destroy` when no lab
// config could be resolved/loaded (wrong cwd, forgotten -c, forgotten
// --git/--git-ref — see the CLAUDE.md note on this bug). Rather than
// guessing a default cluster name that may not correspond to anything
// actually running (the old, silently-wrong behavior), it reuses the same
// live-discovery `astrona list` already does (collectQEMURows/
// collectKindRows — no config needed, qemu handle.json + kind container
// labels are enough) and destroys what it finds:
//   - exactly one non-test lab running: destroy it (best-effort — no config
//     means no teardown scripts to run, and keepCluster can't be honored).
//   - any "astro-test-" leftovers (from a crashed/Ctrl-C'd `astrona test` —
//     see cmd_devtest.go) are always cleaned up best-effort alongside,
//     regardless of count, since those are unconditionally disposable.
//   - zero non-test labs: nothing to destroy, not an error.
//   - 2+ non-test labs: refuse to guess which one — that's a destructive
//     choice this tool won't make silently — and tell the user to pick one
//     with -c/--file/--git.
func destroyByDiscovery() error {
	qemuRows, _, err := collectQEMURows()
	if err != nil {
		return fmt.Errorf("auto-discovery of running labs failed: %w", err)
	}
	rows := append(qemuRows, collectKindRows()...)

	var real, test []labRow
	for _, r := range rows {
		if strings.HasPrefix(r.name, "astro-test-") {
			test = append(test, r)
		} else {
			real = append(real, r)
		}
	}

	if len(real) > 1 {
		var names []string
		for _, r := range real {
			names = append(names, fmt.Sprintf("  %s (%s)", r.name, r.runtime))
		}
		return fmt.Errorf("no lab config found and multiple astrona labs are running — specify which to destroy with -c/--file/--git:\n%s", strings.Join(names, "\n"))
	}

	if len(real) == 0 && len(test) == 0 {
		fmt.Printf("No astrona labs currently running — nothing to destroy.\n")
		return nil
	}

	for _, r := range real {
		fmt.Printf("No lab config found — auto-detected the only running astrona lab: '%s' (%s runtime). Destroying it (teardown scripts skipped, config unknown)...\n", r.name, r.runtime)
		if err := DestroyEnvironment(r.name, RuntimeConfig{Type: r.runtime}); err != nil {
			return fmt.Errorf("failed to destroy '%s': %w", r.name, err)
		}
	}
	for _, r := range test {
		fmt.Printf("Cleaning up leftover test lab '%s' (%s runtime)...\n", r.name, r.runtime)
		if err := DestroyEnvironment(r.name, RuntimeConfig{Type: r.runtime}); err != nil {
			fmt.Printf("[WARN] failed to destroy leftover test lab '%s': %s\n", r.name, err)
		}
	}

	fmt.Printf("Lab cluster cleaned up successfully.\n")
	return nil
}

// destroyByName tears down exactly the named lab, bypassing config
// resolution entirely — e.g. `astrona destroy astro-my-lab` after
// `astrona list` (list already prints the exact prefixed name to pass
// here). No config to read means no teardown scripts and no keepCluster
// check, same trade-off as destroyByDiscovery. Checks qemu state and kind
// clusters directly rather than reusing collectQEMURows (which skips a
// stale/dead qemu VM's leftover state dir) — a name-targeted destroy should
// still clean that up.
func destroyByName(name string) error {
	foundQemu := qemuStateExists(name)
	foundKind := kindClusterExists(name)

	if !foundQemu && !foundKind {
		return fmt.Errorf("no astrona lab named '%s' found (checked qemu state and kind clusters) — run `astrona list` to see what's actually running", name)
	}

	var errs []string
	if foundQemu {
		if err := DestroyQEMUVM(name); err != nil {
			errs = append(errs, fmt.Sprintf("qemu: %s", err))
		}
	}
	if foundKind {
		if err := DeleteKindCluster(name); err != nil {
			errs = append(errs, fmt.Sprintf("kind: %s", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to destroy '%s': %s", name, strings.Join(errs, "; "))
	}

	fmt.Printf("Lab '%s' cleaned up successfully.\n", name)
	return nil
}

// qemuStateExists checks ~/.astrona/qemu/<name>/handle.json directly rather
// than calling qemuStateDir (which MkdirAll's the dir as a side effect —
// wrong for a plain existence check).
func qemuStateExists(name string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".astrona", "qemu", name, "handle.json"))
	return err == nil
}

// kindClusterExists mirrors collectKindRows' own lookup (container engine,
// kind's own cluster label, "-control-plane" suffix) for a single name
// rather than the whole list.
func kindClusterExists(name string) bool {
	engine, err := DetectContainerEngine()
	if err != nil {
		return false
	}

	out, err := exec.Command(engine.Path, "ps", "-a",
		"--filter", "label=io.x-k8s.kind.cluster",
		"--format", "{{.Names}}").Output()
	if err != nil {
		return false
	}

	target := name + "-control-plane"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// teardownEnvironment picks what runs the teardown scripts: the lab's real
// environment if it's still reachable, otherwise the host shell — teardown
// scripts should still get a best-effort run even if the cluster/VM is
// already gone. Wrapped in a LabEnvironment (rather than returning a bare
// ScriptExecutor) so RunInitScripts can still resolve per-VM targeting
// (ResourceItem.VM) for a multi-VM qemu lab's teardown scripts.
func teardownEnvironment(clusterName string, runtimeCfg RuntimeConfig) *LabEnvironment {
	env, err := LoadEnvironment(clusterName, runtimeCfg)
	if err != nil {
		fmt.Printf("[WARN] could not reach lab environment for teardown scripts, running on host instead: %s\n", err)
		return &LabEnvironment{Executor: LocalExecutor{}}
	}
	return env
}

// tearDownLabEnvironment runs teardown scripts (if any) then destroys
// clusterName's environment. hardFail controls whether a destroy failure is
// returned to the caller or just logged — used to make the "test-<lab>"
// side-destroy (see newDestroyCmd) best-effort so a missing/already-gone
// test environment never fails `astrona destroy` for the real one.
func tearDownLabEnvironment(clusterName string, info teardownInfo, baseDir string, hardFail bool) error {
	if len(info.teardown.Init) > 0 {
		fmt.Printf("Running teardown scripts for '%s'...\n", clusterName)
		env := teardownEnvironment(clusterName, info.runtime)
		if err := runOnEveryVM(info.teardown.Init, baseDir, env, info.runtime.QEMU); err != nil {
			fmt.Printf("[WARN] teardown scripts failed for '%s': %s\n", clusterName, err)
		}
	}

	if info.teardown.KeepCluster {
		fmt.Printf("keepCluster is set, leaving cluster '%s' running.\n", clusterName)
		return nil
	}

	if err := DestroyEnvironment(clusterName, info.runtime); err != nil {
		if hardFail {
			return fmt.Errorf("lab teardown failed: %w", err)
		}
		fmt.Printf("[WARN] teardown failed for '%s': %s\n", clusterName, err)
	}

	return nil
}

// newDestroyCmd builds `astrona destroy`: run teardown scripts, then tear
// down the lab environment (unless the config says keepCluster: true).
// Also best-effort tears down the "test-<lab>" environment `astrona test`
// uses (see cmd_devtest.go) — a cancelled `astrona test` skips Go's defer
// cleanup entirely (Ctrl-C kills the process before it runs), so without
// this a crashed test run would need its own separate cleanup command. One
// `astrona destroy` is enough to clean up whichever of the two you actually
// have running. flags is bound to the root command's persistent flags.
func newDestroyCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy [lab-name]",
		Short: "Tear down a lab environment (both the normal run and any leftover 'astrona test' run)",
		Long: "Tear down a lab environment (both the normal run and any leftover 'astrona test' run).\n\n" +
			"With no lab-name, resolves the lab config the same way `run`/`submit` do (-c/--file/--git/--git-ref) " +
			"and falls back to auto-discovering running labs if that fails.\n\n" +
			"With a lab-name (the exact name `astrona list` prints, e.g. 'astro-my-lab'), destroys that lab " +
			"directly — no config needed, so -c/--file/--git/--git-ref are ignored and any teardown scripts are skipped.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return destroyByName(args[0])
			}

			finalPath, err := ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef)
			if err != nil {
				fmt.Printf("[WARN] path resolution failed (%s) — falling back to auto-discovery of running astrona labs\n", err)
				return destroyByDiscovery()
			}

			fmt.Printf("Loading configuration from: %s\n", finalPath)

			baseDir := filepath.Dir(finalPath)
			info, configCleanup, loadErr := loadTeardownInfo(finalPath)
			defer configCleanup()

			if loadErr != nil {
				fmt.Printf("[WARN] could not load lab config from '%s' (%s) — falling back to auto-discovery of running astrona labs\n", finalPath, loadErr)
				return destroyByDiscovery()
			}

			clusterName := normalizeClusterName(info.clusterName)
			if err := tearDownLabEnvironment(clusterName, info, baseDir, true); err != nil {
				return err
			}

			testClusterName := normalizeTestClusterName(info.clusterName)
			if err := tearDownLabEnvironment(testClusterName, info, baseDir, false); err != nil {
				// tearDownLabEnvironment(hardFail=false) never actually
				// returns an error, but handle it rather than silently
				// dropping one if that ever changes.
				fmt.Printf("[WARN] %s\n", err)
			}

			fmt.Printf("Lab cluster cleaned up successfully.\n")
			return nil
		},
	}

	return cmd
}
