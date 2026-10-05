package cluster

import (
	"fmt"
	"os"
	"os/exec"

	"astrona/internal/config"
	"astrona/internal/ui"

	"gopkg.in/yaml.v3"
)

// ContainerEngine is whichever of Docker/Podman was found on PATH — kind
// needs to know which one to drive the local cluster with.
type ContainerEngine struct {
	Name string
	Path string
}

func DetectContainerEngine() (ContainerEngine, error) {
	if path, err := exec.LookPath("docker"); err == nil {
		return ContainerEngine{Name: "docker", Path: path}, nil
	}

	if path, err := exec.LookPath("podman"); err == nil {
		return ContainerEngine{Name: "podman", Path: path}, nil
	}

	return ContainerEngine{}, fmt.Errorf("no container engine found PATH")
}

// kindClusterConfig is the subset of kind's own Cluster config
// (kind.x-k8s.io/v1alpha4) astrona generates from runtime.kind. Only
// fields with a typed counterpart in config.KindConfig exist here — see
// that type for why it's an allowlist.
type kindClusterConfig struct {
	Kind          string            `yaml:"kind"`
	APIVersion    string            `yaml:"apiVersion"`
	Nodes         []kindNode        `yaml:"nodes"`
	Networking    *kindNetworking   `yaml:"networking,omitempty"`
	FeatureGates  map[string]bool   `yaml:"featureGates,omitempty"`
	RuntimeConfig map[string]string `yaml:"runtimeConfig,omitempty"`
}

type kindNode struct {
	Role string `yaml:"role"`
}

type kindNetworking struct {
	DisableDefaultCNI bool   `yaml:"disableDefaultCNI,omitempty"`
	KubeProxyMode     string `yaml:"kubeProxyMode,omitempty"`
	IPFamily          string `yaml:"ipFamily,omitempty"`
	PodSubnet         string `yaml:"podSubnet,omitempty"`
	ServiceSubnet     string `yaml:"serviceSubnet,omitempty"`
}

// BuildKindConfig renders k as a kind cluster config file. Returns nil for
// a nil/empty k — the caller then runs `kind create cluster` without
// --config, exactly as before runtime.kind existed. The node image is not
// part of the file; it's passed as --image (applies to every node).
func BuildKindConfig(k *config.KindConfig) ([]byte, error) {
	if k.IsZero() {
		return nil, nil
	}

	cfg := kindClusterConfig{
		Kind:          "Cluster",
		APIVersion:    "kind.x-k8s.io/v1alpha4",
		FeatureGates:  k.FeatureGates,
		RuntimeConfig: k.RuntimeConfig,
	}
	for range k.ControlPlaneCount() {
		cfg.Nodes = append(cfg.Nodes, kindNode{Role: "control-plane"})
	}
	for range k.WorkerCount() {
		cfg.Nodes = append(cfg.Nodes, kindNode{Role: "worker"})
	}
	if n := k.Networking; n != (config.KindNetworking{}) {
		cfg.Networking = &kindNetworking{
			DisableDefaultCNI: n.DisableDefaultCNI,
			KubeProxyMode:     n.KubeProxyMode,
			IPFamily:          n.IPFamily,
			PodSubnet:         n.PodSubnet,
			ServiceSubnet:     n.ServiceSubnet,
		}
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to render kind cluster config: %w", err)
	}
	return out, nil
}

// kindCreateArgs builds `kind create cluster`'s argument slice.
// configPath is "" when no config file is used.
func kindCreateArgs(clusterName, configPath, nodeImage string) []string {
	args := []string{"create", "cluster", "--name", clusterName}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	if nodeImage != "" {
		args = append(args, "--image", nodeImage)
	}
	return args
}

// writeKindConfig writes data to a private temp file for `kind create
// cluster --config`, returning its path and a cleanup func. No-op ("",
// no-op cleanup) for nil data.
func writeKindConfig(data []byte) (string, func(), error) {
	if data == nil {
		return "", func() {}, nil
	}
	f, err := os.CreateTemp("", "astrona-kind-config-*.yaml")
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to create kind config file: %w", err)
	}
	cleanup := func() { os.Remove(f.Name()) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("failed to write kind config file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("failed to write kind config file: %w", err)
	}
	return f.Name(), cleanup, nil
}

// CreateKindCluster creates clusterName, shaped by k (runtime.kind; nil
// for kind's defaults).
func CreateKindCluster(clusterName string, k *config.KindConfig, rep *ui.Reporter) error {
	t := rep.Step("Detect container engine")
	engine, err := DetectContainerEngine()
	if err != nil {
		return t.Fail(err)
	}

	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return t.Fail(fmt.Errorf("kind not found in PATH: %w", err))
	}
	t.Done()

	label := fmt.Sprintf("Create kind cluster %q (%s)", clusterName, engine.Name)
	if !k.IsZero() {
		label = fmt.Sprintf("Create kind cluster %q (%s; %s)", clusterName, engine.Name, k.Describe())
	}
	t = rep.Step("%s", label)

	data, err := BuildKindConfig(k)
	if err != nil {
		return t.Fail(err)
	}
	configPath, cleanup, err := writeKindConfig(data)
	if err != nil {
		return t.Fail(err)
	}
	defer cleanup()

	out := t.Output()
	if data != nil {
		fmt.Fprintf(out, "kind cluster config:\n%s\n", data)
	}

	cmd := exec.Command(kindPath, kindCreateArgs(clusterName, configPath, k.NodeImage())...)
	cmd.Stdout = out
	cmd.Stderr = out

	cmd.Env = os.Environ()
	if engine.Name == "podman" {
		cmd.Env = append(cmd.Env, "KIND_EXPERIMENTAL_PROVIDER=podman")
	}

	if err := cmd.Run(); err != nil {
		if img := k.NodeImage(); img != "" {
			return t.Fail(fmt.Errorf("%w (node image '%s' — check it exists and matches your kind version: https://github.com/kubernetes-sigs/kind/releases)", err, img))
		}
		return t.Fail(err)
	}
	t.Done()
	return nil
}

func DeleteKindCluster(clusterName string, rep *ui.Reporter) error {
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return fmt.Errorf("kind not found in PATH: %w", err)
	}

	t := rep.Step("Delete kind cluster %q", clusterName)

	cmd := exec.Command(kindPath, "delete", "cluster", "--name", clusterName)
	out := t.Output()
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Run(); err != nil {
		return t.Fail(err)
	}
	if err := RemoveLabState(clusterName); err != nil {
		return t.Fail(err)
	}
	t.Done()
	return nil
}
