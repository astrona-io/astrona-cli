// Package lifecycle brings labs up and takes them down: the one pipeline
// every command uses to create a lab's environment (Up: create, links,
// preload, addons, lab CA, DNS, bootstrap, readiness, port forwards), its
// linked clusters (StartLinkedClusters, in dependency order, optionally in
// parallel), their stable names and WAN conditions, the shared CA,
// teardown of linked clusters, and the soft reset.
//
// It prints progress through a ui.Reporter only; summaries, prompts and
// command-line handling stay in cmd/astrona.
package lifecycle
