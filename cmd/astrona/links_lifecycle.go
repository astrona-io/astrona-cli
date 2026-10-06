package main

import (
	"astrona/internal/cluster"
)

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
