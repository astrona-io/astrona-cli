package lifecycle

import (
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/runtime"
	"astrona/internal/ui"
	"fmt"
	"strings"
)

// LinkedClusterConfig is linked cluster l of cfg as a lab of its own, for
// Up: l's cluster shape and bootstrap, named <lab>-<name>.
func LinkedClusterConfig(cfg *config.LabConfig, l config.KindCluster) *config.LabConfig {
	k := l.Cluster()
	if parent := cfg.Runtime.Kind; parent != nil && parent.SharedCA {
		k.SharedCA, k.CALab = true, parent.CALab
	}
	return &config.LabConfig{
		Metadata:  config.MetadataConfig{Name: cfg.Metadata.Name + "-" + l.Name},
		Runtime:   config.RuntimeConfig{Type: string(runtime.RuntimeKind), Kind: k},
		Bootstrap: l.Bootstrap,
	}
}

// ClusterStates is where cfg's linked clusters are (or will be) for the lab
// running as labCluster, in start order (dependencies first) — the order
// `astrona start` brings them back in.
func ClusterStates(cfg *config.LabConfig, labCluster string) ([]config.KindCluster, []cluster.LinkState, error) {
	order, err := config.KindClusterOrder(cfg.KindClusters())
	if err != nil {
		return nil, nil, err
	}
	var states []cluster.LinkState
	for _, l := range order {
		states = append(states, cluster.LinkState{Name: l.Name, Cluster: config.LinkedClusterName(labCluster, l.Name), WAN: l.WAN})
	}
	return order, states, nil
}

// StartLinkedClusters creates cfg's linked clusters (runtime.kind.clusters) for the
// lab running as labCluster, one at a time in dependency order: each only
// once everything it dependsOn is up and ready (bootstrap and waitFor
// done), with their addresses available to its scripts. The first failure
// stops it — nothing that depends on a broken cluster is started. A
// leftover cluster of the same name (a failed earlier run) is replaced —
// linked clusters belong to the lab, nothing else uses them. The states
// are saved before anything is created, so `astrona destroy` finds every
// cluster even if this fails halfway.
func StartLinkedClusters(cfg *config.LabConfig, baseDir, labCluster string, forTest bool, parallel int, rep *ui.Reporter) ([]cluster.LinkState, error) {
	order, states, err := ClusterStates(cfg, labCluster)
	if err != nil || len(states) == 0 {
		return nil, err
	}
	if err := cluster.WriteLinks(labCluster, states); err != nil {
		return nil, fmt.Errorf("save linked clusters: %w", err)
	}
	if parallel > 1 && len(order) > 1 {
		if err := startLinkedClustersParallel(cfg, order, states, baseDir, forTest, parallel, rep); err != nil {
			return nil, err
		}
		// Clusters created earlier learn the names of later ones.
		RefreshLinkNames(labCluster, true, rep)
		return states, nil
	}
	byName := map[string]cluster.LinkState{}
	for i, l := range order {
		name := states[i].Cluster
		var deps []cluster.LinkState
		for _, d := range l.DependsOn {
			deps = append(deps, byName[d])
		}
		sub := LinkedClusterConfig(cfg, l)
		if err := runtime.DestroyEnvironment(name, sub.Runtime, rep); err != nil {
			rep.Warn("could not clean up a previous '%s', proceeding anyway: %s", name, err)
		}
		rep.Section("Linked cluster '%s'", l.Name)
		_, _, err := Up(sub, baseDir, name, deps, forTest, rep)
		if err == nil {
			err = ApplyWAN(name, l.WAN, rep)
		}
		if err != nil {
			err = linkedClusterError(l, name, err)
			if rest := notStarted(order[i+1:]); rest != "" {
				err = fmt.Errorf("%w — not started: %s", err, rest)
			}
			return nil, err
		}
		byName[l.Name] = states[i]
	}
	// Clusters created earlier learn the names of later ones.
	RefreshLinkNames(labCluster, true, rep)
	return states, nil
}

// linkedClusterError names which linked cluster failed — the same wording
// on the sequential and --parallel paths.
func linkedClusterError(l config.KindCluster, clusterName string, err error) error {
	return fmt.Errorf("linked cluster '%s' (%s): %w", l.Name, clusterName, err)
}

// ApplyWAN applies a linked cluster's wan conditions (none: no-op).
func ApplyWAN(clusterName string, w config.WANConditions, rep *ui.Reporter) error {
	if w.IsZero() {
		return nil
	}
	t := rep.Step("Simulate WAN on %s (%s)", clusterName, DescribeWAN(w))
	if err := cluster.ApplyWAN(clusterName, w); err != nil {
		return t.Fail(err)
	}
	t.Done()
	return nil
}

// DescribeWAN is a short human summary, e.g. "150ms ±20ms, 2% loss".
func DescribeWAN(w config.WANConditions) string {
	var parts []string
	if w.Latency != "" {
		p := w.Latency
		if w.Jitter != "" {
			p += " ±" + w.Jitter
		}
		parts = append(parts, p)
	}
	if w.Loss != "" {
		parts = append(parts, w.Loss+" loss")
	}
	if w.Rate != "" {
		parts = append(parts, w.Rate)
	}
	if len(parts) == 0 {
		return "no conditions"
	}
	return strings.Join(parts, ", ")
}

// notStarted names the clusters a failed start skipped, plus the lab.
func notStarted(rest []config.KindCluster) string {
	names := make([]string, 0, len(rest)+1)
	for _, l := range rest {
		names = append(names, l.Name)
	}
	return strings.Join(append(names, "the lab itself"), ", ")
}
