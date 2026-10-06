package main

import (
	"slices"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/portforward"
	"astrona/internal/scripts"
	"astrona/internal/ui"
)

// ownedClusters lists the linked clusters (runtime.kind.labs) of the lab
// running as labCluster that exist: from its saved state and, when known,
// its config — so a cluster is found even if the lab died before saving.
// Never matched by name prefix: lab "app" must not claim lab "app-idp".
func ownedClusters(labCluster string, labs []config.KindLab) []string {
	var names []string
	add := func(n string) {
		if !slices.Contains(names, n) && kindClusterExists(n) {
			names = append(names, n)
		}
	}
	links, _ := cluster.ReadLinks(labCluster)
	for _, l := range links {
		add(l.Cluster)
	}
	for _, l := range labs {
		add(config.KindLabClusterName(labCluster, l.Name))
	}
	return names
}

// destroyOwnedClusters destroys linked clusters found by ownedClusters,
// warning (not failing) on any it can't remove.
func destroyOwnedClusters(names []string, rep *ui.Reporter) {
	for _, n := range names {
		if err := destroyKindLab(n, rep); err != nil {
			rep.Warn("could not destroy linked cluster %s: %s", n, err)
		}
	}
}

// runLinkedTeardown runs each linked cluster's teardown scripts — reverse
// start order, so a cluster tears down before the ones it depends on —
// with KUBECONFIG pointing at it. Best effort: failures only warn.
func runLinkedTeardown(labs []config.KindLab, labCluster, baseDir string, rep *ui.Reporter) {
	order, err := config.KindLabOrder(labs)
	if err != nil {
		order = labs
	}
	for i := len(order) - 1; i >= 0; i-- {
		l := order[i]
		if len(l.Teardown.Init) == 0 {
			continue
		}
		name := config.KindLabClusterName(labCluster, l.Name)
		// Never fall back to the host's own kubectl context: a script meant
		// for a cluster that's gone must not run against the user's.
		if !kindClusterExists(name) {
			rep.Info("linked cluster '%s' isn't running — skipping its teardown scripts", l.Name)
			continue
		}
		rep.Section("Teardown: cluster %s", l.Name)
		env := teardownEnvironment(name, config.RuntimeConfig{Type: "kind", Kind: l.Cluster()}, rep)
		if err := scripts.RunOnEveryVM(l.Teardown.Init, baseDir, env, nil, rep); err != nil {
			rep.Warn("teardown scripts failed for linked cluster '%s': %s", l.Name, err)
		}
	}
}

// destroyKindLab removes a kind cluster by name, like `astrona destroy
// <name>` (no config, so no teardown scripts).
func destroyKindLab(name string, rep *ui.Reporter) error {
	if err := exam.Clear(name); err != nil {
		rep.Warn("%s", err)
	}
	portforward.StopForLab(name, rep)
	return cluster.DeleteKindCluster(name, rep)
}

// linkedClusterOwners maps every running linked cluster to the lab it
// belongs to — so discovery and lab pickers treat it as part of that lab,
// not as a lab of its own.
func linkedClusterOwners() map[string]string {
	owners := map[string]string{}
	for _, r := range collectKindRows() {
		links, _ := cluster.ReadLinks(r.name)
		for _, l := range links {
			owners[l.Cluster] = r.name
		}
	}
	return owners
}
