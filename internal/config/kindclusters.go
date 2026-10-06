package config

import (
	"fmt"
	"regexp"
	"strings"
)

// KindCluster is one entry in runtime.kind.clusters: an extra kind cluster that
// runs side by side with the lab's own — e.g. an identity provider the app
// under test logs in through. It belongs to the lab: `astrona run` creates
// it first (as <cluster>-<name>), and stop/start/reset/destroy/test handle
// it together with the lab. The lab is told where it is: scripts and
// command checks get ASTRONA_CLUSTER_<NAME>_HOST / _CONTEXT / _KUBECONFIG,
// the cluster gets a ConfigMap astrona-clusters, and a check with
// `cluster: <name>` is graded in it.
//
// All kind clusters share one container network, so a pod in the lab
// reaches a NodePort service of a linked cluster at <HOST>:<nodePort>;
// ClusterIP services and pod IPs aren't routable between clusters.
//
// The cluster fields mirror KindConfig (minus labs — no nesting); see
// Cluster. TestKindClusterMirrorsKindConfig keeps them in step.
type KindCluster struct {
	// Name is how the lab refers to the cluster (env vars, ConfigMap keys,
	// `cluster:` on checks): a short DNS label.
	Name          string            `yaml:"name"`
	Version       string            `yaml:"version"`
	Image         string            `yaml:"image"`
	Nodes         KindNodes         `yaml:"nodes"`
	Networking    KindNetworking    `yaml:"networking"`
	FeatureGates  map[string]bool   `yaml:"featureGates"`
	RuntimeConfig map[string]string `yaml:"runtimeConfig"`
	Addons        KindAddons        `yaml:"addons"`
	PreloadImages []string          `yaml:"preloadImages"`
	// Bootstrap sets the cluster up (init scripts, manifests, waitFor)
	// before the lab's own bootstrap runs. Testing is this cluster's part
	// of the reference solution: `astrona test` applies it after every
	// cluster is bootstrapped, before the lab's own testing. Paths are
	// relative to the lab's config, like everywhere else — by convention
	// under labs/<name>/.
	Bootstrap BootstrapConfig `yaml:"bootstrap"`
	Testing   BootstrapConfig `yaml:"testing"`
	// Teardown runs before the lab is destroyed — after the lab's own
	// teardown, in reverse start order — with KUBECONFIG pointing at this
	// cluster. Best effort, like the lab's.
	Teardown KindClusterTeardown `yaml:"teardown"`
	// WAN simulates a remote site: latency, jitter, loss and a bandwidth
	// cap on everything this cluster sends — see WANConditions (wan.go).
	WAN WANConditions `yaml:"wan"`
	// DependsOn names linked clusters that must be up and ready (bootstrap
	// and waitFor done) before this one is created. Its scripts get their
	// ASTRONA_CLUSTER_* addresses. If one fails, this one isn't started.
	DependsOn []string `yaml:"dependsOn"`
}

// KindClusterTeardown is a linked cluster's teardown: scripts only — whether
// clusters are kept is the lab's teardown.keepCluster.
type KindClusterTeardown struct {
	Init []ResourceItem `yaml:"init"`
}

// Cluster is l's kind cluster shape. Gateway host ports are always
// skipped: they'd clash with the lab's own.
func (l KindCluster) Cluster() *KindConfig {
	k := &KindConfig{
		Version: l.Version, Image: l.Image, Nodes: l.Nodes, Networking: l.Networking,
		FeatureGates: l.FeatureGates, RuntimeConfig: l.RuntimeConfig, Addons: l.Addons,
		PreloadImages: l.PreloadImages,
	}
	k.Addons.SkipHostPorts = true
	return k
}

// EnvPrefix is the environment variable prefix for this cluster, e.g.
// "ASTRONA_CLUSTER_IDP" for name "idp".
func (l KindCluster) EnvPrefix() string {
	return "ASTRONA_CLUSTER_" + strings.ToUpper(strings.ReplaceAll(l.Name, "-", "_"))
}

// LinkedClusterName is the kind cluster name of linked cluster name in
// the lab whose own cluster is labCluster (astro-<lab> or
// astro-test-<lab>).
func LinkedClusterName(labCluster, name string) string {
	return labCluster + "-" + name
}

// KindClusters returns cfg's linked clusters (runtime.kind.clusters).
func (cfg *LabConfig) KindClusters() []KindCluster {
	if cfg.Runtime.Kind == nil {
		return nil
	}
	return cfg.Runtime.Kind.Clusters
}

// moveDeprecatedLabs moves the old runtime.kind.labs into
// runtime.kind.clusters, recording a deprecation. With both set it leaves
// labs in place for ValidateKindClusters to reject.
func (cfg *LabConfig) moveDeprecatedLabs() {
	k := cfg.Runtime.Kind
	if k == nil || len(k.Labs) == 0 || len(k.Clusters) > 0 {
		return
	}
	k.Clusters, k.Labs = k.Labs, nil
	cfg.Deprecations = append(cfg.Deprecations, "runtime.kind.labs is now runtime.kind.clusters — rename it (labs still works for now)")
}

const (
	maxKindClusters = 5
	maxNodeName     = 63
)

var kindClusterNamePattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,18}[a-z0-9])?$`)

// ValidateKindClusters checks runtime.kind.clusters — names, each cluster's shape
// and bootstrap — and that every `cluster:` on a check names one of them.
func ValidateKindClusters(cfg *LabConfig) error {
	if k := cfg.Runtime.Kind; k != nil && len(k.Labs) > 0 {
		return fmt.Errorf("set runtime.kind.clusters, not both clusters and labs (labs is its old name)")
	}
	labs := cfg.KindClusters()
	if len(labs) > maxKindClusters {
		return fmt.Errorf("runtime.kind.clusters has %d entries — at most %d", len(labs), maxKindClusters)
	}
	names := map[string]bool{}
	for _, l := range labs {
		if !kindClusterNamePattern.MatchString(l.Name) {
			return fmt.Errorf("runtime.kind.clusters: name '%s' must be a short lowercase name (a-z, 0-9, '-', max 20, starting with a letter)", l.Name)
		}
		if names[l.Name] {
			return fmt.Errorf("runtime.kind.clusters: duplicate name '%s'", l.Name)
		}
		names[l.Name] = true
		where := "runtime.kind.clusters[" + l.Name + "]"
		// The longest node name is the `astrona test` copy's control plane;
		// node names are hostnames, so at most 63 characters.
		if node := LinkedClusterName(NormalizeTestClusterName(cfg.Metadata.Name), l.Name) + "-control-plane"; len(node) > maxNodeName {
			return fmt.Errorf("%s: node name '%s' would be %d characters (max %d) — shorten metadata.name or the cluster name", where, node, len(node), maxNodeName)
		}
		if err := ValidateKindConfig(RuntimeConfig{Type: "kind", Kind: l.Cluster()}); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		for _, w := range l.Bootstrap.WaitFor {
			if err := w.Validate(where + ".bootstrap"); err != nil {
				return err
			}
		}
		if err := l.WAN.Validate(); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		for _, w := range l.Testing.WaitFor {
			if err := w.Validate(where + ".testing"); err != nil {
				return err
			}
		}
	}
	if _, err := KindClusterOrder(labs); err != nil {
		return err
	}
	return validateCheckClusters(cfg, names)
}

// KindClusterOrder returns labs in start order: every cluster after the ones
// it depends on, otherwise in config order. Errors on an unknown or
// duplicate dependency, a self-dependency, or a cycle.
func KindClusterOrder(labs []KindCluster) ([]KindCluster, error) {
	index := map[string]int{}
	for i, l := range labs {
		index[l.Name] = i
	}
	pending := make([]int, len(labs)) // unmet dependencies per cluster
	for i, l := range labs {
		seen := map[string]bool{}
		for _, d := range l.DependsOn {
			where := "runtime.kind.clusters[" + l.Name + "].dependsOn"
			switch _, ok := index[d]; {
			case d == l.Name:
				return nil, fmt.Errorf("%s: a cluster can't depend on itself", where)
			case !ok:
				return nil, fmt.Errorf("%s: '%s' isn't in runtime.kind.clusters", where, d)
			case seen[d]:
				return nil, fmt.Errorf("%s: '%s' is listed twice", where, d)
			}
			seen[d] = true
			pending[i]++
		}
	}
	done := make([]bool, len(labs))
	var order []KindCluster
	for len(order) < len(labs) {
		next := -1
		for i := range labs {
			if !done[i] && pending[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var stuck []string
			for i, l := range labs {
				if !done[i] {
					stuck = append(stuck, l.Name)
				}
			}
			return nil, fmt.Errorf("runtime.kind.clusters: dependsOn forms a cycle between %s", strings.Join(stuck, ", "))
		}
		picked := labs[next]
		done[next] = true
		order = append(order, picked)
		for i, l := range labs {
			for _, d := range l.DependsOn {
				if d == picked.Name {
					pending[i]--
				}
			}
		}
	}
	return order, nil
}

// validateCheckClusters checks every `cluster:` on a validation check or
// port forward names a linked cluster — a stray one must never silently
// act on the lab's own cluster instead.
func validateCheckClusters(cfg *LabConfig, names map[string]bool) error {
	check := func(where string, checks []ValidationCheck) error {
		for i, c := range checks {
			if c.Cluster == "" {
				continue
			}
			if !names[c.Cluster] {
				return fmt.Errorf("%s.checks[%d] '%s': cluster '%s' isn't in runtime.kind.clusters", where, i, c.Name, c.Cluster)
			}
			if strings.EqualFold(c.Type, "http") {
				return fmt.Errorf("%s.checks[%d] '%s': http checks run from this machine — cluster doesn't apply", where, i, c.Name)
			}
		}
		return nil
	}
	if err := check("validation", cfg.Validation.Checks); err != nil {
		return err
	}
	for _, pf := range cfg.Runtime.PortForwards {
		if pf.Cluster != "" && !names[pf.Cluster] {
			return fmt.Errorf("runtime.portForwards '%s': cluster '%s' isn't in runtime.kind.clusters", pf.Name, pf.Cluster)
		}
	}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil {
			if err := check("runtime.qemu["+vm.Name+"].validation", vm.Validation.Checks); err != nil {
				return err
			}
		}
	}
	return nil
}
