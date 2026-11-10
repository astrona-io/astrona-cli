package lifecycle

import (
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/executor"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"
	"fmt"
	"os"
	"slices"
)

// checkOtherLinkedOwners includes stopped or failed parents: their saved links
// still reserve names even when the child itself is temporarily absent.
func checkOtherLinkedOwners(lab, child string) error {
	dir, err := cluster.KindStateDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == lab {
			continue
		}
		links, err := cluster.ReadLinks(entry.Name())
		if err != nil {
			return fmt.Errorf("read linked ownership for %s: %w", entry.Name(), err)
		}
		for _, link := range links {
			if link.Cluster == child {
				return fmt.Errorf("linked cluster %s is reserved by lab %s; choose another name or explicitly destroy that owning lab", child, entry.Name())
			}
		}
	}
	return nil
}

// HasLinkedOwnership checks saved links without requiring the child to exist,
// so an interrupted linked reset can recreate its missing child. A standalone
// run under the child name supersedes an older parent's ownership record.
func HasLinkedOwnership(lab, child string) (bool, error) {
	links, err := cluster.ReadLinks(lab)
	if err != nil {
		return false, err
	}
	st, err := labstate.Load(child)
	if err != nil {
		return false, err
	}
	if st != nil {
		return false, nil
	}
	if err := checkOtherLinkedOwners(lab, child); err != nil {
		return false, err
	}
	for _, link := range links {
		if link.Cluster == child && child == config.LinkedClusterName(lab, link.Name) {
			return true, nil
		}
	}
	return false, nil
}

// RequireLinkedOwnership rejects an inferred or superseded child name.
func RequireLinkedOwnership(lab, child string) error {
	owned, err := HasLinkedOwnership(lab, child)
	if err != nil {
		return fmt.Errorf("check linked cluster %s ownership: %w", child, err)
	}
	if !owned {
		return fmt.Errorf("linked cluster %s has no saved ownership for %s; nothing was reset", child, lab)
	}
	return nil
}

// OwnedClusters lists the linked clusters (runtime.kind.clusters) of the lab
// running as labCluster that exist and have saved ownership. A config alone
// cannot claim an existing cluster; setup saves links before creating any.
func OwnedClusters(labCluster string, _ []config.KindCluster) []string {
	var names []string
	add := func(n string) {
		owned, err := HasLinkedOwnership(labCluster, n)
		if err == nil && owned && !slices.Contains(names, n) && cluster.Exists(n) {
			names = append(names, n)
		}
	}
	links, _ := cluster.ReadLinks(labCluster)
	for _, l := range links {
		if l.Cluster == config.LinkedClusterName(labCluster, l.Name) {
			add(l.Cluster)
		}
	}
	return names
}

// DestroyOwnedClusters destroys linked clusters found by OwnedClusters,
// warning (not failing) on any it can't remove.
func DestroyOwnedClusters(lab string, names []string, rep *ui.Reporter) {
	for _, n := range names {
		release, err := labstate.LockMutation(n)
		if err != nil {
			rep.Warn("could not destroy linked cluster %s: %s", n, err)
			continue
		}
		st, err := labstate.Load(n)
		if err != nil || st != nil || checkOtherLinkedOwners(lab, n) != nil {
			release()
			rep.Warn("kept linked cluster %s: its saved state no longer identifies it as a linked cluster", n)
			continue
		}
		if err := DestroyKindCluster(n, rep); err != nil {
			rep.Warn("could not destroy linked cluster %s: %s", n, err)
		}
		release()
	}
}

// RunLinkedTeardown runs each linked cluster's teardown scripts — reverse
// start order, so a cluster tears down before the ones it depends on —
// with KUBECONFIG pointing at it. Best effort: failures only warn.
func RunLinkedTeardown(labs []config.KindCluster, labCluster, baseDir string, rep *ui.Reporter) {
	owned := OwnedClusters(labCluster, nil)
	order, err := config.KindClusterOrder(labs)
	if err != nil {
		order = labs
	}
	for i := len(order) - 1; i >= 0; i-- {
		l := order[i]
		if len(l.Teardown.Init) == 0 {
			continue
		}
		name := config.LinkedClusterName(labCluster, l.Name)
		if !slices.Contains(owned, name) {
			rep.Info("linked cluster '%s' has no saved ownership — skipping its teardown scripts", l.Name)
			continue
		}
		release, err := labstate.LockMutation(name)
		if err != nil {
			rep.Warn("skipping teardown scripts for linked cluster '%s': %s", l.Name, err)
			continue
		}
		owned, err := HasLinkedOwnership(labCluster, name)
		if err != nil || !owned {
			release()
			rep.Warn("skipping teardown scripts for linked cluster '%s': its saved state changed", l.Name)
			continue
		}
		// Never fall back to the host's own kubectl context: a script meant
		// for a cluster that's gone must not run against the user's.
		if !cluster.Exists(name) {
			rep.Info("linked cluster '%s' isn't running — skipping its teardown scripts", l.Name)
			release()
			continue
		}
		rep.Section("Teardown: cluster %s", l.Name)
		env := TeardownEnvironment(name, config.RuntimeConfig{Type: "kind", Kind: l.Cluster()}, rep)
		if err := scripts.RunOnEveryVM(l.Teardown.Init, baseDir, env, nil, rep); err != nil {
			rep.Warn("teardown scripts failed for linked cluster '%s': %s", l.Name, err)
		}
		release()
	}
}

// DestroyKindCluster removes a kind cluster by name, like `astrona destroy
// <name>` (no config, so no teardown scripts).
// The exam clock is cleared only once the cluster is deleted.
func DestroyKindCluster(name string, rep *ui.Reporter) error {
	portforward.StopForLab(name, rep)
	if err := cluster.DeleteKindCluster(name, rep); err != nil {
		return err
	}
	if err := exam.Clear(name); err != nil {
		rep.Warn("%s", err)
	}
	return nil
}

// TeardownEnvironment picks what runs the teardown scripts: the lab's real
// environment if it still exists, otherwise the host shell — teardown
// scripts should still get a best-effort run (host-side cleanup) even if
// the cluster/VM is already gone. That fallback gets KUBECONFIG=/dev/null:
// a script's `kubectl delete …` must never land on the user's own current
// context instead of the lab. Wrapped in a LabEnvironment (rather than
// returning a bare ScriptExecutor) so RunInitScripts can still resolve
// per-VM targeting (ResourceItem.VM) for a multi-VM qemu lab's teardown
// scripts.
//
// A qemu lab's scripts are written for (and approved to run) inside its
// VMs, never on the host: when its VMs are gone or unreachable this returns
// nil and the caller skips the scripts.
func TeardownEnvironment(clusterName string, runtimeCfg config.RuntimeConfig, rep *ui.Reporter) *runtime.LabEnvironment {
	qemu := runtimeCfg.Type == string(runtime.RuntimeQEMU)
	hostOnly := &runtime.LabEnvironment{Executor: executor.LocalExecutor{Kubeconfig: os.DevNull}}
	if !EnvironmentExists(clusterName, runtimeCfg) {
		if qemu {
			rep.Warn("lab VM(s) for '%s' don't exist — skipping teardown scripts (they only run inside the VMs)", clusterName)
			return nil
		}
		rep.Warn("lab environment '%s' doesn't exist — running teardown scripts on the host, without cluster access", clusterName)
		return hostOnly
	}
	env, err := runtime.LoadEnvironment(clusterName, runtimeCfg)
	if err != nil {
		if qemu {
			rep.Warn("could not reach the lab VM(s) for teardown scripts, skipping them (they only run inside the VMs): %s", err)
			return nil
		}
		rep.Warn("could not reach lab environment for teardown scripts, running on the host without cluster access instead: %s", err)
		return hostOnly
	}
	return env
}

// EnvironmentExists reports whether clusterName's kind cluster or qemu VM
// state exists.
func EnvironmentExists(clusterName string, runtimeCfg config.RuntimeConfig) bool {
	if runtimeCfg.Type == string(runtime.RuntimeQEMU) {
		return hypervisor.StateExists(clusterName) || hypervisor.StateExists(clusterName+"-"+firstVMName(runtimeCfg))
	}
	return cluster.Exists(clusterName)
}

func firstVMName(rt config.RuntimeConfig) string {
	if len(rt.QEMU) == 0 {
		return ""
	}
	return rt.QEMU[0].Name
}
