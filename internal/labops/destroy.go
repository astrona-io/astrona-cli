package labops

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"
	"astrona/internal/vmprovider/qemu"
)

// Teardown is what `astrona destroy` needs from the lab config — best
// effort, since destroy must still run even if the config is missing or
// unreadable (e.g. the lab directory was already cleaned up).
type Teardown struct {
	ClusterName string
	RunID       string    // when set, only remove this saved run, checked under the mutation lock
	Status      io.Writer // nil uses DestroyOptions.Out; submit JSON cleanup writes notices to stderr
	Teardown    config.TeardownConfig
	Runtime     config.RuntimeConfig
	Config      *config.LabConfig // the loaded config, for the trust check
	SkipScripts bool              // the lab isn't trusted: destroy it without running its scripts
}

// DestroyOptions are the caller's writers and choices for a destroy.
type DestroyOptions struct {
	// Out gets the outcome lines ("cleaned up successfully") and the
	// kubectl-context notice — stdout in cmd.
	Out io.Writer
	// ForgetWarn gets ForgetLab's warnings — stderr in cmd.
	ForgetWarn io.Writer
	// Forget is how the destroyed lab's memory is forgotten
	// (--keep-resources, ending its session).
	Forget ForgetOptions
}

func (o DestroyOptions) forget(name string) { ForgetLab(o.ForgetWarn, name, o.Forget) }

// LoadTeardown tries to load finalPath for the extra info destroy can use
// (cluster name, teardown scripts, runtime type). It returns the load
// error rather than swallowing it — a missing/unreadable config means
// destroy can't trust the fallback "astrona-lab" name (nothing may actually
// be running under that name), so the caller must fall back to discovery
// (DestroyByDiscovery) instead of silently "destroying" a name that was
// never real.
func LoadTeardown(finalPath string) (Teardown, func(), error) {
	info := Teardown{ClusterName: "astrona-lab"}
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
		info.ClusterName = cfg.Metadata.Name
	}
	info.Teardown = cfg.Teardown
	info.Runtime = cfg.Runtime
	info.Config = cfg

	return info, cleanup, nil
}

// DestroyByDiscovery is the fallback path for `astrona destroy` when no lab
// config could be resolved/loaded (wrong cwd, forgotten -c, forgotten
// --git/--git-ref). Rather than guessing a default cluster name that may
// not correspond to anything actually running (the old, silently-wrong
// behavior), it reuses the same live-discovery `astrona list` already does
// (CollectQEMURows/LookupKindRows — no config needed, qemu handle.json +
// kind container labels are enough) and destroys what it finds:
//   - exactly one non-test lab running: destroy it (best-effort — no config
//     means no teardown scripts to run, and keepCluster can't be honored).
//   - any "astro-test-" leftovers (from a crashed/Ctrl-C'd `astrona test`)
//     are cleaned up best-effort alongside, regardless of count. An active
//     test holds its mutation lock and is left alone.
//   - zero non-test labs: nothing to destroy, not an error.
//   - 2+ non-test labs: refuse to guess which one — that's a destructive
//     choice this tool won't make silently — and tell the user to pick one
//     with -c/--file/--git.
func DestroyByDiscovery(rep *ui.Reporter, opts DestroyOptions) error {
	// Capture run identities before querying runtimes. A run that appears or
	// changes during discovery must not inherit an earlier cleanup decision.
	names, err := labstate.Names()
	if err != nil {
		return err
	}
	runs := make(map[string]string, len(names))
	for _, name := range names {
		st, err := labstate.Load(name)
		if err != nil {
			return err
		}
		runs[name] = RunID(st)
	}
	qemuRows, _, err := CollectQEMURows()
	if err != nil {
		return fmt.Errorf("auto-discovery of running labs failed: %w", err)
	}
	kindRows, err := LookupKindRows()
	if err != nil {
		// Its kind labs are hidden, so "the only running lab" can't be known.
		return fmt.Errorf("auto-discovery of running labs failed — the container engine is unreachable: %w\nNothing was destroyed. Start Docker or Podman and run destroy again", err)
	}
	rows := append(qemuRows, kindRows...)

	// A linked cluster goes with its lab (DestroyByName), not on its own.
	owners := LinkedClusterOwners()
	var realLabs, test []LabRow
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Runtime == "qemu" && r.Lab != "" {
			r.Name = r.Lab
		}
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		if _, linked := owners[r.Name]; linked {
			continue
		}
		if strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) {
			test = append(test, r)
		} else {
			realLabs = append(realLabs, r)
		}
	}

	if len(realLabs) > 1 {
		var names []string
		for _, r := range realLabs {
			names = append(names, fmt.Sprintf("  %s (%s)", r.Name, r.Runtime))
		}
		return fmt.Errorf("no lab config found and multiple astrona labs are running — specify which to destroy with -c/--file/--git:\n%s", strings.Join(names, "\n"))
	}

	if len(realLabs) == 0 && len(test) == 0 {
		fmt.Fprintf(opts.Out, "No astrona labs currently running — nothing to destroy.\n")
		return nil
	}

	for _, r := range realLabs {
		rep.Info("No lab config found — auto-detected the only running astrona lab: '%s' (%s runtime). Destroying it (teardown scripts skipped, config unknown).", r.Name, r.Runtime)
		if err := destroyDiscoveredLab(r.Name, runs[r.Name], rep, opts); err != nil {
			return fmt.Errorf("failed to destroy '%s': %w", r.Name, err)
		}
	}
	for _, r := range test {
		rep.Info("Cleaning up leftover test lab '%s' (%s runtime).", r.Name, r.Runtime)
		if err := destroyDiscoveredLab(r.Name, runs[r.Name], rep, opts); err != nil {
			rep.Warn("failed to destroy leftover test lab '%s': %s", r.Name, err)
		}
	}

	fmt.Fprintf(opts.Out, "Lab cluster cleaned up successfully.\n")
	return nil
}

func destroyDiscoveredLab(name, runID string, rep *ui.Reporter, opts DestroyOptions) error {
	release, err := labstate.LockMutation(name)
	if err != nil {
		return err
	}
	defer release()
	st, err := labstate.Load(name)
	if err != nil {
		return err
	}
	if RunID(st) != runID {
		return fmt.Errorf("lab %s changed during discovery; its current run was kept", name)
	}
	return DestroyByNameLocked(name, rep, opts)
}

// DestroyByName tears down exactly the named lab under its mutation lock,
// bypassing config resolution entirely — e.g. `astrona destroy
// astro-my-lab` after `astrona list` (list already prints the exact
// prefixed name to pass here). No config to read means no teardown scripts
// and no keepCluster check, same trade-off as DestroyByDiscovery. Checks
// qemu state and kind clusters directly rather than reusing
// CollectQEMURows (which skips a stale/dead qemu VM's leftover state dir) —
// a name-targeted destroy should still clean that up. A multi-VM qemu
// lab's name destroys all its VMs (found by the lab recorded in each VM's
// handle).
func DestroyByName(name string, rep *ui.Reporter, opts DestroyOptions) error {
	name = config.NormalizeClusterName(name)
	release, err := labstate.LockMutation(name)
	if err != nil {
		return err
	}
	defer release()
	return DestroyByNameLocked(name, rep, opts)
}

// DestroyByNameLocked is DestroyByName for a caller that already holds the
// lab's mutation lock.
func DestroyByNameLocked(name string, rep *ui.Reporter, opts DestroyOptions) error {
	// Accept the lab name with or without the "astro-" prefix — `astrona
	// list` prints the prefixed form, but a user typing the bare lab name
	// should still hit the right lab. Idempotent, so an already-prefixed
	// name (including names passed from DestroyByPattern) is unchanged.
	name = config.NormalizeClusterName(name)
	if err := config.ValidateName(name); err != nil {
		return fmt.Errorf("lab: %w", err)
	}

	foundQemu := hypervisor.StateExists(name)
	vms := hypervisor.MultiVMs(name) // a multi-VM qemu lab's VMs: <name>-<vm>
	// A VM state dir without handle.json, left by an older astrona: its
	// disks would otherwise stay forever.
	legacyQemu := !foundQemu && hypervisor.LegacyStateExists(name)
	foundKind, err := cluster.Lookup(name)
	if err != nil {
		if !foundQemu && !legacyQemu && len(vms) == 0 {
			// Unknown is not absent: keep everything for the retry.
			return EngineUnreachable(name, err)
		}
		// Its qemu state names it a qemu lab; only kind is unknown.
		rep.Warn("container engine unreachable — destroying the qemu lab '%s' without checking for a kind cluster of the same name: %s", name, err)
	}
	owned := lifecycle.OwnedClusters(name, nil) // read before destroy removes the saved state
	// Supervisors are local processes: stop them whatever runtime is found.
	portforward.StopForLab(name, rep)

	if !foundQemu && !legacyQemu && len(vms) == 0 && !foundKind && len(owned) == 0 {
		// Confirmed absent: whatever was remembered about it is stale.
		clearExamClock(name, rep)
		opts.forget(name)
		return fmt.Errorf("no astrona lab named '%s' found (checked qemu state and kind clusters) — run `astrona list` to see what's actually running", name)
	}

	ReleaseLab(opts.Out, name) // before the delete unsets the lab's context

	var errs []string
	vmp := qemu.New(rep)
	if foundQemu || legacyQemu {
		if err := vmp.Delete(context.Background(), name); err != nil {
			errs = append(errs, fmt.Sprintf("qemu: %s", err))
		}
	}
	for _, vm := range vms {
		if err := destroyOwnedQEMUVM(name, vm, rep); err != nil {
			errs = append(errs, fmt.Sprintf("qemu %s: %s", vm, err))
		}
	}
	if foundKind {
		if err := cluster.DeleteKindCluster(name, rep); err != nil {
			errs = append(errs, fmt.Sprintf("kind: %s", err))
		}
	}
	lifecycle.DestroyOwnedClusters(name, owned, rep)
	if len(errs) > 0 {
		return fmt.Errorf("failed to destroy '%s': %s", name, strings.Join(errs, "; "))
	}
	clearExamClock(name, rep) // only now that the lab is gone
	opts.forget(name)

	fmt.Fprintf(opts.Out, "Lab '%s' cleaned up successfully.\n", name)
	return nil
}

// A child may have been removed and recreated as a standalone lab since
// MultiVMs listed it. Recheck ownership while its name is reserved.
func destroyOwnedQEMUVM(lab, vm string, rep *ui.Reporter) error {
	release, err := labstate.LockMutation(vm)
	if err != nil {
		return err
	}
	defer release()
	st, err := labstate.Load(vm)
	if err != nil {
		return err
	}
	if st != nil {
		return fmt.Errorf("qemu VM %s now has standalone lab state; it was kept", vm)
	}
	if err := hypervisor.RequireQEMUOwnership(vm, lab); err != nil {
		return err
	}
	return qemu.New(rep).Delete(context.Background(), vm)
}

// DestroyByPattern handles a glob-style lab-name argument (e.g.
// 'astro-qemu-jumphost-*') — matched with filepath.Match against every
// currently-running lab name (qemu + kind, same discovery `astrona list`
// uses), then torn down one by one via DestroyByName. Refuses to guess
// silently: when labs are running but none match, that's an error, not a
// no-op, since a typo'd glob should never look like a successful cleanup.
// But when nothing is running at all, there's nothing to typo against —
// that's just "nothing to destroy", same as DestroyByDiscovery.
func DestroyByPattern(pattern string, rep *ui.Reporter, opts DestroyOptions) error {
	qemuRows, _, err := CollectQEMURows()
	if err != nil {
		return fmt.Errorf("auto-discovery of running labs failed: %w", err)
	}
	kindRows, err := LookupKindRows()
	if err != nil {
		return fmt.Errorf("can't match '%s' — the container engine is unreachable: %w\nNothing was destroyed. Start Docker or Podman and run destroy again", pattern, err)
	}
	rows := append(qemuRows, kindRows...)

	if len(rows) == 0 {
		fmt.Fprintf(opts.Out, "No astrona labs currently running — nothing to destroy (pattern '%s' had nothing to match against).\n", pattern)
		return nil
	}

	var matched []string
	for _, r := range rows {
		ok, err := filepath.Match(pattern, r.Name)
		if err != nil {
			return fmt.Errorf("invalid pattern '%s': %w", pattern, err)
		}
		if !ok {
			// Also match against the name with the "astro-" prefix
			// stripped, so `destroy 'my-lab-*'` behaves the same as
			// `destroy 'astro-my-lab-*'`.
			ok, _ = filepath.Match(pattern, strings.TrimPrefix(r.Name, apphome.ClusterPrefix()))
		}
		if ok {
			matched = append(matched, r.Name)
		}
	}

	if len(matched) == 0 {
		var running []string
		for _, r := range rows {
			running = append(running, fmt.Sprintf("  %s (%s)", r.Name, r.Runtime))
		}
		return fmt.Errorf("pattern '%s' matched none of the %d running astrona lab(s) — nothing was destroyed. Currently running:\n%s",
			pattern, len(rows), strings.Join(running, "\n"))
	}

	fmt.Fprintf(opts.Out, "Pattern '%s' matched %d lab(s): %s\n", pattern, len(matched), strings.Join(matched, ", "))

	var errs []string
	for _, name := range matched {
		// Already gone as a linked cluster of an earlier match.
		if !hypervisor.StateExists(name) && !cluster.Exists(name) {
			continue
		}
		if err := DestroyByName(name, rep, opts); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to destroy %d of %d matched lab(s):\n%s", len(errs), len(matched), strings.Join(errs, "\n"))
	}

	return nil
}

// EngineUnreachable is destroy's stop when the container engine can't say
// whether name's kind cluster exists. Nothing has been changed.
func EngineUnreachable(name string, err error) error {
	return fmt.Errorf("cannot tell whether lab '%s' still exists: %w\nNothing was destroyed; its saved state, session and exam clock are kept. Start Docker or Podman and run destroy again", name, err)
}

// clearExamClock removes name's exam clock once its lab is gone.
func clearExamClock(name string, rep *ui.Reporter) {
	if err := exam.Clear(name); err != nil {
		rep.Warn("%s", err)
	}
}

// HasTeardownScripts reports whether destroying the lab would run any of
// its scripts — its own teardown.init or a linked cluster's.
func HasTeardownScripts(info Teardown) bool {
	if len(info.Teardown.Init) > 0 {
		return true
	}
	if info.Runtime.Kind != nil {
		for _, l := range info.Runtime.Kind.Clusters {
			if len(l.Teardown.Init) > 0 {
				return true
			}
		}
	}
	return false
}

// TearDownLabEnvironment runs teardown scripts (if any) then destroys
// clusterName's environment. hardFail controls whether a destroy failure is
// returned to the caller or just logged — used to make the "test-<lab>"
// side-destroy (see DestroyLab) best-effort so a missing/already-gone test
// environment never fails `astrona destroy` for the real one.
func TearDownLabEnvironment(clusterName string, info Teardown, baseDir string, hardFail bool, rep *ui.Reporter) error {
	// The side-destroy of a test copy that isn't there has nothing to tear
	// down — don't run the lab's teardown scripts for it.
	if !hardFail && !lifecycle.EnvironmentExists(clusterName, info.Runtime) {
		// No cluster or VM, but a forward supervisor of it may still run.
		portforward.StopForLab(clusterName, rep)
		return nil
	}
	var labs []config.KindCluster
	if info.Runtime.Kind != nil {
		labs = info.Runtime.Kind.Clusters
	}
	if !info.SkipScripts {
		if len(info.Teardown.Init) > 0 {
			rep.Section("Teardown: %s", clusterName)
			if env := lifecycle.TeardownEnvironment(clusterName, info.Runtime, rep); env != nil {
				if err := scripts.RunOnEveryVM(info.Teardown.Init, baseDir, env, info.Runtime.QEMU, rep); err != nil {
					rep.Warn("teardown scripts failed for '%s': %s", clusterName, err)
				}
			}
		}
		lifecycle.RunLinkedTeardown(labs, clusterName, baseDir, rep)
	}

	if info.Teardown.KeepCluster {
		rep.Info("keepCluster is set, leaving cluster '%s' running.", clusterName)
		return nil
	}

	// Whatever the runtime, and even when no environment is found (a kind
	// cluster removed behind astrona's back, a config that no longer
	// resolves): a forward supervisor must never outlive its lab.
	portforward.StopForLab(clusterName, rep)
	owned := lifecycle.OwnedClusters(clusterName, labs) // read before destroy removes the saved state
	err := runtime.DestroyEnvironment(clusterName, info.Runtime, rep)
	lifecycle.DestroyOwnedClusters(clusterName, owned, rep) // even when the lab itself failed — never leak them
	if err != nil {
		if hardFail {
			return fmt.Errorf("lab teardown failed: %w", err)
		}
		rep.Warn("teardown failed for '%s': %s", clusterName, err)
	}

	return nil
}

// DestroyLab is `astrona destroy` for a lab whose config is loaded, under
// the lab's mutation lock: the user's kubectl context is put back,
// teardown scripts run (unless info.SkipScripts), the lab and any leftover
// `astrona test` copy are destroyed, and what was remembered about the lab
// is forgotten. A lab with teardown.keepCluster stays — and so do its
// context and memory.
func DestroyLab(info Teardown, baseDir string, rep *ui.Reporter, opts DestroyOptions) error {
	release, err := labstate.LockMutation(config.NormalizeClusterName(info.ClusterName))
	if err != nil {
		return err
	}
	defer release()
	clusterName := config.NormalizeClusterName(info.ClusterName)
	if info.RunID != "" {
		current, err := labstate.Load(clusterName)
		if err != nil {
			return err
		}
		if RunID(current) != info.RunID {
			return fmt.Errorf("lab stopped or restarted during submission; the current run was not deleted")
		}
	}
	if info.Runtime.Type != string(runtime.RuntimeQEMU) {
		// A stopped engine hides the cluster: don't run teardown scripts
		// without it or forget a lab that may still be there.
		if _, err := cluster.Lookup(clusterName); err != nil {
			return EngineUnreachable(clusterName, err)
		}
	}
	status := info.Status
	if status == nil {
		status = opts.Out
	}
	if !info.Teardown.KeepCluster {
		// Before the delete unsets the lab's context. The record is dropped
		// here even though the delete may still fail: by then it has done
		// its job (the context is back, or the user had moved on, or the
		// previous one is gone), and a kept record would only make the
		// retry report "moved on" for the context it just restored.
		ReleaseLab(status, clusterName)
	}
	if err := TearDownLabEnvironment(clusterName, info, baseDir, true, rep); err != nil {
		return err
	}
	if !info.Teardown.KeepCluster {
		opts.forget(clusterName)
	}

	testClusterName := config.NormalizeTestClusterName(info.ClusterName)
	releaseTest, err := labstate.LockMutation(testClusterName)
	if err != nil {
		rep.Warn("kept test lab '%s': %s", testClusterName, err)
		return nil
	}
	defer releaseTest()
	if err := TearDownLabEnvironment(testClusterName, info, baseDir, false, rep); err != nil {
		// TearDownLabEnvironment(hardFail=false) never actually returns an
		// error, but handle it rather than silently dropping one if that
		// ever changes.
		rep.Warn("%s", err)
	}
	return nil
}
