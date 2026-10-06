package config

import (
	"fmt"
	"regexp"
	"strings"
)

// KindLab is one entry in runtime.kind.labs: an extra kind cluster that
// runs side by side with the lab's own — e.g. an identity provider the app
// under test logs in through. It belongs to the lab: `astrona run` creates
// it first (as <cluster>-<name>), and stop/start/reset/destroy/test handle
// it together with the lab. The lab is told where it is: scripts and
// command checks get ASTRONA_LINK_<NAME>_HOST / _CONTEXT / _KUBECONFIG,
// the cluster gets a ConfigMap astrona-links, and a check with
// `cluster: <name>` is graded in it.
//
// All kind clusters share one container network, so a pod in the lab
// reaches a NodePort service of a linked cluster at <HOST>:<nodePort>;
// ClusterIP services and pod IPs aren't routable between clusters.
//
// The cluster fields mirror KindConfig (minus labs — no nesting); see
// Cluster. TestKindLabMirrorsKindConfig keeps them in step.
type KindLab struct {
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
	// DependsOn names linked clusters that must be up and ready (bootstrap
	// and waitFor done) before this one is created. Its scripts get their
	// ASTRONA_LINK_* addresses. If one fails, this one isn't started.
	DependsOn []string `yaml:"dependsOn"`
}

// Cluster is l's kind cluster shape. Gateway host ports are always
// skipped: they'd clash with the lab's own.
func (l KindLab) Cluster() *KindConfig {
	k := &KindConfig{
		Version: l.Version, Image: l.Image, Nodes: l.Nodes, Networking: l.Networking,
		FeatureGates: l.FeatureGates, RuntimeConfig: l.RuntimeConfig, Addons: l.Addons,
		PreloadImages: l.PreloadImages,
	}
	k.Addons.SkipHostPorts = true
	return k
}

// EnvPrefix is the environment variable prefix for this cluster, e.g.
// "ASTRONA_LINK_IDP" for name "idp".
func (l KindLab) EnvPrefix() string {
	return "ASTRONA_LINK_" + strings.ToUpper(strings.ReplaceAll(l.Name, "-", "_"))
}

// KindLabClusterName is the kind cluster name of linked cluster name in
// the lab whose own cluster is labCluster (astro-<lab> or
// astro-test-<lab>).
func KindLabClusterName(labCluster, name string) string {
	return labCluster + "-" + name
}

// KindLabs returns cfg's linked clusters (runtime.kind.labs).
func (cfg *LabConfig) KindLabs() []KindLab {
	if cfg.Runtime.Kind == nil {
		return nil
	}
	return cfg.Runtime.Kind.Labs
}

const (
	maxKindLabs = 5
	maxNodeName = 63
)

var kindLabNamePattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,18}[a-z0-9])?$`)

// ValidateKindLabs checks runtime.kind.labs — names, each cluster's shape
// and bootstrap — and that every `cluster:` on a check names one of them.
func ValidateKindLabs(cfg *LabConfig) error {
	labs := cfg.KindLabs()
	if len(labs) > maxKindLabs {
		return fmt.Errorf("runtime.kind.labs has %d entries — at most %d", len(labs), maxKindLabs)
	}
	names := map[string]bool{}
	for _, l := range labs {
		if !kindLabNamePattern.MatchString(l.Name) {
			return fmt.Errorf("runtime.kind.labs: name '%s' must be a short lowercase name (a-z, 0-9, '-', max 20, starting with a letter)", l.Name)
		}
		if names[l.Name] {
			return fmt.Errorf("runtime.kind.labs: duplicate name '%s'", l.Name)
		}
		names[l.Name] = true
		where := "runtime.kind.labs[" + l.Name + "]"
		// The longest node name is the `astrona test` copy's control plane;
		// node names are hostnames, so at most 63 characters.
		if node := KindLabClusterName(NormalizeTestClusterName(cfg.Metadata.Name), l.Name) + "-control-plane"; len(node) > maxNodeName {
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
		for _, w := range l.Testing.WaitFor {
			if err := w.Validate(where + ".testing"); err != nil {
				return err
			}
		}
	}
	if _, err := KindLabOrder(labs); err != nil {
		return err
	}
	return validateCheckClusters(cfg, names)
}

// KindLabOrder returns labs in start order: every cluster after the ones
// it depends on, otherwise in config order. Errors on an unknown or
// duplicate dependency, a self-dependency, or a cycle.
func KindLabOrder(labs []KindLab) ([]KindLab, error) {
	index := map[string]int{}
	for i, l := range labs {
		index[l.Name] = i
	}
	pending := make([]int, len(labs)) // unmet dependencies per cluster
	for i, l := range labs {
		seen := map[string]bool{}
		for _, d := range l.DependsOn {
			where := "runtime.kind.labs[" + l.Name + "].dependsOn"
			switch _, ok := index[d]; {
			case d == l.Name:
				return nil, fmt.Errorf("%s: a cluster can't depend on itself", where)
			case !ok:
				return nil, fmt.Errorf("%s: '%s' isn't in runtime.kind.labs", where, d)
			case seen[d]:
				return nil, fmt.Errorf("%s: '%s' is listed twice", where, d)
			}
			seen[d] = true
			pending[i]++
		}
	}
	done := make([]bool, len(labs))
	var order []KindLab
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
			return nil, fmt.Errorf("runtime.kind.labs: dependsOn forms a cycle between %s", strings.Join(stuck, ", "))
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

// validateCheckClusters checks every `cluster:` on a validation check names
// a linked cluster — a stray one must never be silently graded against
// the lab's own cluster instead.
func validateCheckClusters(cfg *LabConfig, names map[string]bool) error {
	check := func(where string, checks []ValidationCheck) error {
		for i, c := range checks {
			if c.Cluster == "" {
				continue
			}
			if !names[c.Cluster] {
				return fmt.Errorf("%s.checks[%d] '%s': cluster '%s' isn't in runtime.kind.labs", where, i, c.Name, c.Cluster)
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
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil {
			if err := check("runtime.qemu["+vm.Name+"].validation", vm.Validation.Checks); err != nil {
				return err
			}
		}
	}
	return nil
}
