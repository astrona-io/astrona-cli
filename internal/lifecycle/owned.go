package lifecycle

import (
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/executor"
	"astrona/internal/hypervisor"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"
	"os"
	"slices"
)

// OwnedClusters lists the linked clusters (runtime.kind.clusters) of the lab
// running as labCluster that exist: from its saved state and, when known,
// its config — so a cluster is found even if the lab died before saving.
// Never matched by name prefix: lab "app" must not claim lab "app-idp".
func OwnedClusters(labCluster string, labs []config.KindCluster) []string {
	var names []string
	add := func(n string) {
		if !slices.Contains(names, n) && cluster.Exists(n) {
			names = append(names, n)
		}
	}
	links, _ := cluster.ReadLinks(labCluster)
	for _, l := range links {
		add(l.Cluster)
	}
	for _, l := range labs {
		add(config.LinkedClusterName(labCluster, l.Name))
	}
	return names
}

// DestroyOwnedClusters destroys linked clusters found by OwnedClusters,
// warning (not failing) on any it can't remove.
func DestroyOwnedClusters(names []string, rep *ui.Reporter) {
	for _, n := range names {
		if err := DestroyKindCluster(n, rep); err != nil {
			rep.Warn("could not destroy linked cluster %s: %s", n, err)
		}
	}
}

// RunLinkedTeardown runs each linked cluster's teardown scripts — reverse
// start order, so a cluster tears down before the ones it depends on —
// with KUBECONFIG pointing at it. Best effort: failures only warn.
func RunLinkedTeardown(labs []config.KindCluster, labCluster, baseDir string, rep *ui.Reporter) {
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
		// Never fall back to the host's own kubectl context: a script meant
		// for a cluster that's gone must not run against the user's.
		if !cluster.Exists(name) {
			rep.Info("linked cluster '%s' isn't running — skipping its teardown scripts", l.Name)
			continue
		}
		rep.Section("Teardown: cluster %s", l.Name)
		env := TeardownEnvironment(name, config.RuntimeConfig{Type: "kind", Kind: l.Cluster()}, rep)
		if err := scripts.RunOnEveryVM(l.Teardown.Init, baseDir, env, nil, rep); err != nil {
			rep.Warn("teardown scripts failed for linked cluster '%s': %s", l.Name, err)
		}
	}
}

// DestroyKindCluster removes a kind cluster by name, like `astrona destroy
// <name>` (no config, so no teardown scripts).
func DestroyKindCluster(name string, rep *ui.Reporter) error {
	if err := exam.Clear(name); err != nil {
		rep.Warn("%s", err)
	}
	portforward.StopForLab(name, rep)
	return cluster.DeleteKindCluster(name, rep)
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
func TeardownEnvironment(clusterName string, runtimeCfg config.RuntimeConfig, rep *ui.Reporter) *runtime.LabEnvironment {
	hostOnly := &runtime.LabEnvironment{Executor: executor.LocalExecutor{Kubeconfig: os.DevNull}}
	if !EnvironmentExists(clusterName, runtimeCfg) {
		rep.Warn("lab environment '%s' doesn't exist — running teardown scripts on the host, without cluster access", clusterName)
		return hostOnly
	}
	env, err := runtime.LoadEnvironment(clusterName, runtimeCfg)
	if err != nil {
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
