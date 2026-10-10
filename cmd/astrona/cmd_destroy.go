package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/hypervisor"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// teardownInfo is what `astrona destroy` needs from the lab config — best
// effort, since destroy must still run even if the config is missing or
// unreadable (e.g. the lab directory was already cleaned up).
type teardownInfo struct {
	clusterName string
	teardown    config.TeardownConfig
	runtime     config.RuntimeConfig
	cfg         *config.LabConfig // the loaded config, for the trust check
	skipScripts bool              // the lab isn't trusted: destroy it without running its scripts
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

	cfg, configCleanup, err := config.LoadLabConfig(finalPath)
	if err != nil {
		return info, cleanup, err
	}
	cleanup = configCleanup

	if cfg.Metadata.Name != "" {
		info.clusterName = cfg.Metadata.Name
	}
	info.teardown = cfg.Teardown
	info.runtime = cfg.Runtime
	info.cfg = cfg

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
func destroyByDiscovery(rep *ui.Reporter) error {
	qemuRows, _, err := collectQEMURows()
	if err != nil {
		return fmt.Errorf("auto-discovery of running labs failed: %w", err)
	}
	rows := append(qemuRows, collectKindRows()...)

	// A linked cluster goes with its lab (destroyByName), not on its own.
	owners := linkedClusterOwners()
	var realLabs, test []labRow
	for _, r := range rows {
		if _, linked := owners[r.name]; linked {
			continue
		}
		if strings.HasPrefix(r.name, "astro-test-") {
			test = append(test, r)
		} else {
			realLabs = append(realLabs, r)
		}
	}

	if len(realLabs) > 1 {
		var names []string
		for _, r := range realLabs {
			names = append(names, fmt.Sprintf("  %s (%s)", r.name, r.runtime))
		}
		return fmt.Errorf("no lab config found and multiple astrona labs are running — specify which to destroy with -c/--file/--git:\n%s", strings.Join(names, "\n"))
	}

	if len(realLabs) == 0 && len(test) == 0 {
		fmt.Printf("No astrona labs currently running — nothing to destroy.\n")
		return nil
	}

	for _, r := range realLabs {
		rep.Info("No lab config found — auto-detected the only running astrona lab: '%s' (%s runtime). Destroying it (teardown scripts skipped, config unknown).", r.name, r.runtime)
		owned := lifecycle.OwnedClusters(r.name, nil)
		releaseLab(os.Stdout, r.name) // before the delete unsets the lab's context
		err := runtime.DestroyEnvironment(r.name, config.RuntimeConfig{Type: r.runtime}, rep)
		lifecycle.DestroyOwnedClusters(owned, rep) // even when the lab itself failed — never leak them
		if err != nil {
			return fmt.Errorf("failed to destroy '%s': %w", r.name, err)
		}
		forgetLab(r.name)
	}
	for _, r := range test {
		rep.Info("Cleaning up leftover test lab '%s' (%s runtime).", r.name, r.runtime)
		owned := lifecycle.OwnedClusters(r.name, nil)
		if err := runtime.DestroyEnvironment(r.name, config.RuntimeConfig{Type: r.runtime}, rep); err != nil {
			rep.Warn("failed to destroy leftover test lab '%s': %s", r.name, err)
		}
		lifecycle.DestroyOwnedClusters(owned, rep)
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
// still clean that up. A multi-VM qemu lab's name destroys all its VMs
// (found by the lab recorded in each VM's handle).
func destroyByName(name string, rep *ui.Reporter) error {
	// Accept the lab name with or without the "astro-" prefix — `astrona
	// list` prints the prefixed form, but a user typing the bare lab name
	// should still hit the right lab. Idempotent, so an already-prefixed
	// name (including names passed from destroyByPattern) is unchanged.
	name = config.NormalizeClusterName(name)
	if err := config.ValidateName(name); err != nil {
		return fmt.Errorf("lab: %w", err)
	}

	foundQemu := qemuStateExists(name)
	vms := hypervisor.MultiVMs(name) // a multi-VM qemu lab's VMs: <name>-<vm>
	foundKind := kindClusterExists(name)
	owned := lifecycle.OwnedClusters(name, nil) // read before destroy removes the saved state
	if err := exam.Clear(name); err != nil {
		rep.Warn("%s", err)
	}

	if !foundQemu && len(vms) == 0 && !foundKind && len(owned) == 0 {
		forgetLab(name) // whatever was remembered about it is stale
		return fmt.Errorf("no astrona lab named '%s' found (checked qemu state and kind clusters) — run `astrona list` to see what's actually running", name)
	}

	releaseLab(os.Stdout, name) // before the delete unsets the lab's context

	var errs []string
	if foundQemu {
		if err := hypervisor.DestroyQEMUVM(name, rep); err != nil {
			errs = append(errs, fmt.Sprintf("qemu: %s", err))
		}
	}
	for _, vm := range vms {
		if err := hypervisor.DestroyQEMUVM(vm, rep); err != nil {
			errs = append(errs, fmt.Sprintf("qemu %s: %s", vm, err))
		}
	}
	if foundKind {
		portforward.StopForLab(name, rep)
		if err := cluster.DeleteKindCluster(name, rep); err != nil {
			errs = append(errs, fmt.Sprintf("kind: %s", err))
		}
	}
	lifecycle.DestroyOwnedClusters(owned, rep)
	if len(errs) > 0 {
		return fmt.Errorf("failed to destroy '%s': %s", name, strings.Join(errs, "; "))
	}
	forgetLab(name)

	fmt.Printf("Lab '%s' cleaned up successfully.\n", name)
	return nil
}

// destroyByPattern handles a glob-style lab-name argument (e.g.
// 'astro-qemu-jumphost-*') — matched with filepath.Match against every
// currently-running lab name (qemu + kind, same discovery `astrona list`
// uses), then torn down one by one via destroyByName. Refuses to guess
// silently: when labs are running but none match, that's an error, not a
// no-op, since a typo'd glob should never look like a successful cleanup.
// But when nothing is running at all, there's nothing to typo against —
// that's just "nothing to destroy", same as destroyByDiscovery.
func destroyByPattern(pattern string, rep *ui.Reporter) error {
	qemuRows, _, err := collectQEMURows()
	if err != nil {
		return fmt.Errorf("auto-discovery of running labs failed: %w", err)
	}
	rows := append(qemuRows, collectKindRows()...)

	if len(rows) == 0 {
		fmt.Printf("No astrona labs currently running — nothing to destroy (pattern '%s' had nothing to match against).\n", pattern)
		return nil
	}

	var matched []string
	for _, r := range rows {
		ok, err := filepath.Match(pattern, r.name)
		if err != nil {
			return fmt.Errorf("invalid pattern '%s': %w", pattern, err)
		}
		if !ok {
			// Also match against the name with the "astro-" prefix
			// stripped, so `destroy 'my-lab-*'` behaves the same as
			// `destroy 'astro-my-lab-*'`.
			ok, _ = filepath.Match(pattern, strings.TrimPrefix(r.name, "astro-"))
		}
		if ok {
			matched = append(matched, r.name)
		}
	}

	if len(matched) == 0 {
		var running []string
		for _, r := range rows {
			running = append(running, fmt.Sprintf("  %s (%s)", r.name, r.runtime))
		}
		return fmt.Errorf("pattern '%s' matched none of the %d running astrona lab(s) — nothing was destroyed. Currently running:\n%s",
			pattern, len(rows), strings.Join(running, "\n"))
	}

	fmt.Printf("Pattern '%s' matched %d lab(s): %s\n", pattern, len(matched), strings.Join(matched, ", "))

	var errs []string
	for _, name := range matched {
		// Already gone as a linked cluster of an earlier match.
		if !qemuStateExists(name) && !kindClusterExists(name) {
			continue
		}
		if err := destroyByName(name, rep); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to destroy %d of %d matched lab(s):\n%s", len(errs), len(matched), strings.Join(errs, "\n"))
	}

	return nil
}

// qemuStateExists and kindClusterExists: see hypervisor.StateExists and
// cluster.Exists.
func qemuStateExists(name string) bool { return hypervisor.StateExists(name) }

func kindClusterExists(name string) bool { return cluster.Exists(name) }

// hasTeardownScripts reports whether destroying the lab would run any of its
// scripts — its own teardown.init or a linked cluster's.
func hasTeardownScripts(info teardownInfo) bool {
	if len(info.teardown.Init) > 0 {
		return true
	}
	if info.runtime.Kind != nil {
		for _, l := range info.runtime.Kind.Clusters {
			if len(l.Teardown.Init) > 0 {
				return true
			}
		}
	}
	return false
}

// teardownTrusted applies run's trust check before destroy runs a lab's
// teardown scripts. Unlike run it never fails: destroy is best-effort, so an
// unapproved remote lab (refused, or no terminal and no --trust) is still
// destroyed — only its scripts are skipped. Labs with no teardown scripts
// aren't asked about at all; local labs are trusted as everywhere else.
func teardownTrusted(flags *rootFlags, info teardownInfo, baseDir string, rep *ui.Reporter) bool {
	if info.cfg == nil || !hasTeardownScripts(info) {
		return true
	}
	err := requireTrust(flags, info.cfg, baseDir)
	switch {
	case err == nil:
		return true
	case errors.Is(err, errNotTrusted):
		rep.Warn("not trusted — skipping the lab's teardown scripts and destroying it without them (pass --trust to run them)")
	default:
		rep.Warn("skipping the lab's teardown scripts: %s — destroying it without them", err) // no terminal: err names --trust
	}
	return false
}

// tearDownLabEnvironment runs teardown scripts (if any) then destroys
// clusterName's environment. hardFail controls whether a destroy failure is
// returned to the caller or just logged — used to make the "test-<lab>"
// side-destroy (see newDestroyCmd) best-effort so a missing/already-gone
// test environment never fails `astrona destroy` for the real one.
func tearDownLabEnvironment(clusterName string, info teardownInfo, baseDir string, hardFail bool, rep *ui.Reporter) error {
	// The side-destroy of a test copy that isn't there has nothing to tear
	// down — don't run the lab's teardown scripts for it.
	if !hardFail && !lifecycle.EnvironmentExists(clusterName, info.runtime) {
		return nil
	}
	var labs []config.KindCluster
	if info.runtime.Kind != nil {
		labs = info.runtime.Kind.Clusters
	}
	if !info.skipScripts {
		if len(info.teardown.Init) > 0 {
			rep.Section("Teardown: %s", clusterName)
			if env := lifecycle.TeardownEnvironment(clusterName, info.runtime, rep); env != nil {
				if err := scripts.RunOnEveryVM(info.teardown.Init, baseDir, env, info.runtime.QEMU, rep); err != nil {
					rep.Warn("teardown scripts failed for '%s': %s", clusterName, err)
				}
			}
		}
		lifecycle.RunLinkedTeardown(labs, clusterName, baseDir, rep)
	}

	if info.teardown.KeepCluster {
		rep.Info("keepCluster is set, leaving cluster '%s' running.", clusterName)
		return nil
	}

	owned := lifecycle.OwnedClusters(clusterName, labs) // read before destroy removes the saved state
	err := runtime.DestroyEnvironment(clusterName, info.runtime, rep)
	lifecycle.DestroyOwnedClusters(owned, rep) // even when the lab itself failed — never leak them
	if err != nil {
		if hardFail {
			return fmt.Errorf("lab teardown failed: %w", err)
		}
		rep.Warn("teardown failed for '%s': %s", clusterName, err)
	}

	return nil
}

// destroyLab is `astrona destroy` for a lab whose config is loaded: the
// user's kubectl context is put back, teardown scripts run (unless
// info.skipScripts), the lab and any leftover `astrona test` copy are
// destroyed, and what was remembered about the lab is forgotten. A lab with
// teardown.keepCluster stays — and so do its context and memory.
func destroyLab(info teardownInfo, baseDir string, rep *ui.Reporter) error {
	clusterName := config.NormalizeClusterName(info.clusterName)
	if !info.teardown.KeepCluster {
		// Before the delete unsets the lab's context. The record is dropped
		// here even though the delete may still fail: by then it has done
		// its job (the context is back, or the user had moved on, or the
		// previous one is gone), and a kept record would only make the
		// retry report "moved on" for the context it just restored.
		releaseLab(os.Stdout, clusterName)
	}
	if err := tearDownLabEnvironment(clusterName, info, baseDir, true, rep); err != nil {
		return err
	}
	if !info.teardown.KeepCluster {
		forgetLab(clusterName)
	}

	testClusterName := config.NormalizeTestClusterName(info.clusterName)
	if err := tearDownLabEnvironment(testClusterName, info, baseDir, false, rep); err != nil {
		// tearDownLabEnvironment(hardFail=false) never actually
		// returns an error, but handle it rather than silently
		// dropping one if that ever changes.
		rep.Warn("%s", err)
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
		Use:               "destroy [lab-name|pattern]",
		ValidArgsFunction: labCompletion(nil),
		Short:             "Remove a lab and everything in it (and any leftover `astrona test` copy)",
		Long: "Tear down a lab environment (both the normal run and any leftover 'astrona test' run).\n\n" +
			"With no lab-name, resolves the lab config the same way `run`/`submit` do (-c/--file/--git/--git-ref) " +
			"and falls back to auto-discovering running labs if that fails.\n\n" +
			"With a lab-name (as shown by `astrona list`, with or without the 'astro-' prefix — e.g. 'astro-my-lab' " +
			"or just 'my-lab'), destroys that lab directly — no config needed, so -c/--file/--git/--git-ref are " +
			"ignored and any teardown scripts are skipped.\n\n" +
			"With a catalog name (astrona.io/ATS016/section-020/module-01/lab-01, or without astrona.io/ as `astrona labs` lists it), destroys that " +
			"lab through its config, teardown scripts included.\n\n" +
			"With a glob pattern (contains *, ?, or [), matches against all currently-running lab names " +
			"(same list `astrona list` shows, with or without the 'astro-' prefix) and destroys every match — " +
			"e.g. `astrona destroy 'qemu-jumphost-*'` (quote it so your shell doesn't expand the glob itself). " +
			"No config needed, same trade-offs as a single name.\n\n" +
			"A lab's linked clusters (runtime.kind.clusters) are destroyed with it.\n\n" +
			"If `astrona run` pointed kubectl at the lab, the kubectl context you had before comes " +
			"back — only when the lab's context is still current (if you've switched elsewhere, " +
			"that's left as it is). What astrona remembered about the lab (its config, its lab " +
			"session) is forgotten.\n\n" +
			"A remote lab (--git, URL, catalog) that has teardown scripts asks for approval first, like `run` — " +
			"--trust approves it up front. If it isn't approved (declined, or no terminal and no --trust), " +
			"its teardown scripts are skipped and the lab is still destroyed.\n\n" +
			"The lab's copy of its resources (~/.astrona/resources/<lab>) is removed too; " +
			"--keep-resources keeps it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			labName := "-"
			if len(args) == 1 {
				labName = args[0]
			}
			rep, err := ui.NewReporter("destroy", labName, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			// A catalog name (ATS016/…/lab-01) destroys that lab through its
			// config, exactly like `astrona destroy` with -c/--git.
			if len(args) == 1 {
				name, err := catalogLabArg(args[0], flags)
				if err != nil {
					return err
				}
				if name == "" {
					args = nil
				}
			}

			if len(args) == 1 {
				if strings.ContainsAny(args[0], "*?[") {
					return destroyByPattern(args[0], rep)
				}
				return destroyByName(args[0], rep)
			}

			finalPath, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, flags.verbose)
			if err != nil {
				rep.Warn("path resolution failed (%s) — falling back to auto-discovery of running astrona labs", err)
				return destroyByDiscovery(rep)
			}

			rep.Info("Loading configuration from: %s", finalPath)

			baseDir := filepath.Dir(finalPath)
			info, configCleanup, loadErr := loadTeardownInfo(finalPath)
			defer configCleanup()

			if loadErr != nil {
				rep.Warn("could not load lab config from '%s' (%s) — falling back to auto-discovery of running astrona labs", finalPath, loadErr)
				return destroyByDiscovery(rep)
			}

			// A remote lab's teardown scripts are its code too: approve
			// them like run does, but never let a refusal stop the destroy.
			info.skipScripts = !teardownTrusted(flags, info, baseDir, rep)

			if err := destroyLab(info, baseDir, rep); err != nil {
				return err
			}

			rep.Close()
			fmt.Printf("Lab cluster cleaned up successfully.\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&keepResources, "keep-resources", false, "Keep the lab's resources in ~/.astrona/resources/<lab> (for reference)")
	return cmd
}
