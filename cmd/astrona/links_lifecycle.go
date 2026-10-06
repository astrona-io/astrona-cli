package main

import (
	"slices"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/portforward"
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
