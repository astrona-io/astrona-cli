package config

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
)

// KindConfig is runtime.kind: the shape of the kind cluster a lab gets.
// Omitted entirely, a lab gets exactly what it always did — `kind create
// cluster` with kind's own defaults (one control-plane node, kind's default
// node image, kindnet).
//
// Deliberately a small, typed allowlist rather than a passthrough of kind's
// own config file. A lab config can come from an arbitrary URL or git repo,
// and kind's full config can bind-mount host paths into the nodes
// (extraMounts), publish node ports on 0.0.0.0 (extraPortMappings), and
// inject arbitrary kubeadm patches — none of which a remote lab should be
// able to do to the student's machine.
type KindConfig struct {
	// Version picks the node image kindest/node:<version>, e.g. "v1.31.2".
	// Mutually exclusive with Image.
	Version string `yaml:"version"`
	// Image overrides the node image completely — use it to pin by digest
	// (kindest/node:v1.31.2@sha256:…) for reproducible labs.
	Image      string         `yaml:"image"`
	Nodes      KindNodes      `yaml:"nodes"`
	Networking KindNetworking `yaml:"networking"`
	// FeatureGates are Kubernetes feature gates enabled/disabled cluster-wide.
	FeatureGates map[string]bool `yaml:"featureGates"`
	// RuntimeConfig toggles API groups/versions (kube-apiserver
	// --runtime-config), e.g. {"resource.k8s.io/v1beta1": "true"}.
	RuntimeConfig map[string]string `yaml:"runtimeConfig"`
	// Addons are installed right after the cluster is created — see
	// KindAddons (addons.go).
	Addons KindAddons `yaml:"addons"`
	// PreloadImages are pulled on the host (if missing) and loaded into
	// every node right after the cluster is created, so pods using them
	// start without a registry round-trip — faster, no Docker Hub rate
	// limits, offline once the host has them. See preload.go.
	PreloadImages []string `yaml:"preloadImages"`
	// Clusters are extra kind clusters that run side by side with this one
	// — see KindCluster (kindclusters.go).
	Clusters []KindCluster `yaml:"clusters"`
	// Labs is the old name of Clusters (astrona ≤ v0.2.1). Still read —
	// moved into Clusters when the config is loaded, with a deprecation
	// warning.
	Labs []KindCluster `yaml:"labs"`
	// SharedCA gives the lab its own certificate authority, installed in
	// its cluster and every linked cluster (ConfigMap astrona-ca with
	// ca.crt; with cert-manager, a ClusterIssuer astrona-ca), so TLS
	// between them verifies. Lab-wide: set it here, not on a linked
	// cluster.
	SharedCA bool `yaml:"sharedCA"`
	// CALab is set at run time, not in YAML: the lab cluster whose CA this
	// cluster installs (the lab's own name, also for its linked clusters).
	CALab string `yaml:"-"`
}

// KindNodes is the cluster's node count. Zero values mean kind's defaults
// (one control plane, no workers).
type KindNodes struct {
	ControlPlanes int `yaml:"controlPlanes"`
	Workers       int `yaml:"workers"`
}

type KindNetworking struct {
	// DisableDefaultCNI skips kindnet so the lab can install its own CNI
	// (Calico, Cilium, …) in bootstrap. Nodes stay NotReady until it does.
	DisableDefaultCNI bool   `yaml:"disableDefaultCNI"`
	KubeProxyMode     string `yaml:"kubeProxyMode"` // iptables | ipvs | nftables | none
	IPFamily          string `yaml:"ipFamily"`      // ipv4 | ipv6 | dual
	PodSubnet         string `yaml:"podSubnet"`
	ServiceSubnet     string `yaml:"serviceSubnet"`
}

const (
	maxKindControlPlanes = 3
	// maxKindWorkers keeps a lab from asking for more node containers than
	// a laptop's container engine VM can realistically run.
	maxKindWorkers = 6
)

var (
	kindVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	// imageRefPattern is a conservative OCI reference: registry/repo path,
	// optional tag, optional sha256 digest. No whitespace, no leading '-'.
	imageRefPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(:[0-9]+)?(/[a-z0-9][a-z0-9._-]*)*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127})?(@sha256:[a-f0-9]{64})?$`)
	featureGatePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{1,127}$`)
	// runtimeConfigKeyPattern covers "api/all", "api/alpha", and
	// "<group>/<version>" / "<group>/<version>/<resource>" keys.
	runtimeConfigKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*(/[a-z0-9][a-z0-9.-]*){1,2}$`)
)

var (
	validKubeProxyModes = map[string]bool{"": true, "iptables": true, "ipvs": true, "nftables": true, "none": true}
	validIPFamilies     = map[string]bool{"": true, "ipv4": true, "ipv6": true, "dual": true}
)

// IsZero reports whether runtime.kind was left out entirely (or written
// with nothing in it) — the "plain `kind create cluster`" case.
func (k *KindConfig) IsZero() bool {
	return k == nil ||
		(k.Version == "" && k.Image == "" && k.Nodes == KindNodes{} && k.Networking == KindNetworking{} &&
			len(k.FeatureGates) == 0 && len(k.RuntimeConfig) == 0 && k.Addons.IsZero() && len(k.PreloadImages) == 0 &&
			len(k.Clusters) == 0 && len(k.Labs) == 0 && !k.SharedCA)
}

// NodeImage is the image to boot every node from, or "" for kind's default.
func (k *KindConfig) NodeImage() string {
	switch {
	case k == nil:
		return ""
	case k.Image != "":
		return k.Image
	case k.Version != "":
		return "kindest/node:" + k.Version
	default:
		return ""
	}
}

// ControlPlaneCount / WorkerCount apply kind's defaults to zero values.
func (k *KindConfig) ControlPlaneCount() int {
	if k == nil || k.Nodes.ControlPlanes == 0 {
		return 1
	}
	return k.Nodes.ControlPlanes
}

func (k *KindConfig) WorkerCount() int {
	if k == nil {
		return 0
	}
	return k.Nodes.Workers
}

// ValidateKindConfig checks runtime.kind against rt's runtime type and
// every field's allowed values. A nil/empty block is always valid.
func ValidateKindConfig(rt RuntimeConfig) error {
	k := rt.Kind
	if k.IsZero() {
		return nil
	}
	if rt.Type != "" && rt.Type != "kind" {
		return fmt.Errorf("runtime.kind is only valid for the kind runtime (this lab uses '%s')", rt.Type)
	}

	if k.Version != "" && k.Image != "" {
		return fmt.Errorf("runtime.kind: set either version or image, not both")
	}
	if k.Version != "" && !kindVersionPattern.MatchString(k.Version) {
		return fmt.Errorf("runtime.kind.version '%s' must look like 'v1.31.2'", k.Version)
	}
	if k.Image != "" && !imageRefPattern.MatchString(k.Image) {
		return fmt.Errorf("runtime.kind.image '%s' is not a valid image reference", k.Image)
	}

	if n := k.Nodes.ControlPlanes; n < 0 || n > maxKindControlPlanes {
		return fmt.Errorf("runtime.kind.nodes.controlPlanes %d must be between 1 and %d", n, maxKindControlPlanes)
	}
	if n := k.Nodes.Workers; n < 0 || n > maxKindWorkers {
		return fmt.Errorf("runtime.kind.nodes.workers %d must be between 0 and %d", n, maxKindWorkers)
	}

	if err := validateKindNetworking(k.Networking); err != nil {
		return err
	}
	if err := validateKindAddons(rt); err != nil {
		return err
	}
	if err := validatePreloadImages(k.PreloadImages); err != nil {
		return err
	}

	for _, name := range sortedKeys(k.FeatureGates) {
		if !featureGatePattern.MatchString(name) {
			return fmt.Errorf("runtime.kind.featureGates: '%s' is not a valid feature gate name (e.g. 'InPlacePodVerticalScaling')", name)
		}
	}
	for _, key := range sortedKeys(k.RuntimeConfig) {
		if !runtimeConfigKeyPattern.MatchString(key) {
			return fmt.Errorf("runtime.kind.runtimeConfig: '%s' is not a valid API key (e.g. 'api/alpha' or 'resource.k8s.io/v1beta1')", key)
		}
		if v := k.RuntimeConfig[key]; v != "true" && v != "false" {
			return fmt.Errorf("runtime.kind.runtimeConfig['%s'] must be \"true\" or \"false\", got '%s'", key, v)
		}
	}

	return nil
}

func validateKindNetworking(n KindNetworking) error {
	if !validKubeProxyModes[n.KubeProxyMode] {
		return fmt.Errorf("runtime.kind.networking.kubeProxyMode '%s' must be iptables, ipvs, nftables or none", n.KubeProxyMode)
	}
	if !validIPFamilies[n.IPFamily] {
		return fmt.Errorf("runtime.kind.networking.ipFamily '%s' must be ipv4, ipv6 or dual", n.IPFamily)
	}

	var pod, svc *net.IPNet
	if n.PodSubnet != "" {
		var err error
		if _, pod, err = net.ParseCIDR(n.PodSubnet); err != nil {
			return fmt.Errorf("runtime.kind.networking.podSubnet '%s' is not a valid CIDR: %w", n.PodSubnet, err)
		}
	}
	if n.ServiceSubnet != "" {
		var err error
		if _, svc, err = net.ParseCIDR(n.ServiceSubnet); err != nil {
			return fmt.Errorf("runtime.kind.networking.serviceSubnet '%s' is not a valid CIDR: %w", n.ServiceSubnet, err)
		}
	}
	if pod != nil && svc != nil && (pod.Contains(svc.IP) || svc.Contains(pod.IP)) {
		return fmt.Errorf("runtime.kind.networking: podSubnet %s and serviceSubnet %s overlap", n.PodSubnet, n.ServiceSubnet)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Describe is a one-line summary for progress output, e.g.
// "kindest/node:v1.31.2, 1 control plane + 2 workers, no default CNI".
func (k *KindConfig) Describe() string {
	parts := []string{}
	if img := k.NodeImage(); img != "" {
		parts = append(parts, img)
	}
	nodes := fmt.Sprintf("%d control plane", k.ControlPlaneCount())
	if k.ControlPlaneCount() > 1 {
		nodes += "s"
	}
	if w := k.WorkerCount(); w > 0 {
		nodes += fmt.Sprintf(" + %d worker", w)
		if w > 1 {
			nodes += "s"
		}
	}
	parts = append(parts, nodes)
	switch {
	case k != nil && k.Addons.CNI != "":
		parts = append(parts, k.Addons.CNI+" CNI")
	case k != nil && k.Networking.DisableDefaultCNI:
		parts = append(parts, "no default CNI")
	}
	return strings.Join(parts, ", ")
}
