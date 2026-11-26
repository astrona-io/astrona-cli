package main

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/workspace"
)

// nodeListTimeout bounds the one container engine call browserEnvironment
// makes while the run record is written (never while the page polls).
const nodeListTimeout = 5 * time.Second

// browserEnvironment describes what the lab runs on for the browser
// workspace's environment panel: the lab's own Kind cluster (not its
// linked ones) or its VMs. It comes from the lab config, plus one
// container engine call for a Kind lab that lists the node containers as
// they are; what that call cannot tell stays empty, never guessed.
func browserEnvironment(cfg *config.LabConfig, name string) *workspace.Environment {
	if isQEMULab(cfg) {
		return vmEnvironment(cfg, name)
	}
	k := cfg.Runtime.Kind
	env := &workspace.Environment{
		Runtime:   workspace.RuntimeKind,
		Context:   "kind-" + name,
		Namespace: "default",
		Connect:   "kubectl --context kind-" + name + " get nodes",
		Network:   kindNetwork(k),
		Images:    []string{},
		VMs:       []workspace.EnvVM{},
	}
	if k != nil {
		env.Images = append(env.Images, k.PreloadImages...)
	}
	image := ""
	if k != nil {
		image = k.NodeImage()
	}
	nodes, engine, nodeImage, err := kindNodes(name)
	if err == nil {
		env.Engine, env.Nodes = engine, nodes
		if image == "" {
			image = nodeImage
		}
	} else {
		env.Nodes = configuredKindNodes(k, name)
	}
	env.KubernetesVersion = kindestVersion(image)
	return env
}

// kindNetwork is the cluster's CNI by display name: Calico from the addon,
// kindnet by default, "" when the lab installs its own in bootstrap.
func kindNetwork(k *config.KindConfig) string {
	switch {
	case k != nil && k.Addons.CNI == config.CNICalico:
		return "Calico"
	case k != nil && k.Networking.DisableDefaultCNI:
		return ""
	default:
		return "kindnet"
	}
}

// kindNodes lists the cluster's node containers (the HA load balancer is
// not a node) with the engine that runs them and the control plane's image.
func kindNodes(clusterName string) (nodes []workspace.EnvNode, engine, image string, err error) {
	e, err := cluster.DetectContainerEngine()
	if err != nil {
		return nil, "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), nodeListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, e.Path, "ps", "-a",
		"--filter", "label=io.x-k8s.kind.cluster="+clusterName,
		"--format", `{{.Names}}|{{.Label "io.x-k8s.kind.role"}}|{{.Image}}`).Output()
	if err != nil {
		return nil, "", "", fmt.Errorf("%s ps: %w", e.Name, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 3 || parts[0] == "" {
			continue
		}
		if parts[1] != "control-plane" && parts[1] != "worker" {
			continue
		}
		nodes = append(nodes, workspace.EnvNode{Name: parts[0], Role: parts[1]})
		if parts[1] == "control-plane" && image == "" {
			image = parts[2]
		}
	}
	if len(nodes) == 0 {
		return nil, "", "", fmt.Errorf("no node containers of %s", clusterName)
	}
	sortEnvNodes(nodes)
	return nodes, e.Name, image, nil
}

// sortEnvNodes puts control planes first, each role in name order.
func sortEnvNodes(nodes []workspace.EnvNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if (nodes[i].Role == "control-plane") != (nodes[j].Role == "control-plane") {
			return nodes[i].Role == "control-plane"
		}
		return nodes[i].Name < nodes[j].Name
	})
}

// configuredKindNodes are the nodes the config asks for, by kind's naming
// (<cluster>-control-plane, <cluster>-control-plane2, <cluster>-worker,
// <cluster>-worker2, …), when the engine could not be asked.
func configuredKindNodes(k *config.KindConfig, clusterName string) []workspace.EnvNode {
	var nodes []workspace.EnvNode
	add := func(role string, n int) {
		for i := 1; i <= n; i++ {
			name := clusterName + "-" + role
			if i > 1 {
				name += fmt.Sprint(i)
			}
			nodes = append(nodes, workspace.EnvNode{Name: name, Role: role})
		}
	}
	add("control-plane", k.ControlPlaneCount())
	add("worker", k.WorkerCount())
	return nodes
}

// kindestTag is a kindest/node image's Kubernetes version tag.
var kindestTag = regexp.MustCompile(`(?:^|/)kindest/node:(v[0-9]+\.[0-9]+\.[0-9]+)(?:@sha256:[a-f0-9]{64})?$`)

// kindestVersion is the Kubernetes version of a kindest/node image
// ("v1.35.0"); "" for any other image, whose tag says nothing certain.
func kindestVersion(image string) string {
	if m := kindestTag.FindStringSubmatch(strings.TrimSpace(image)); m != nil {
		return m[1]
	}
	return ""
}

func vmEnvironment(cfg *config.LabConfig, name string) *workspace.Environment {
	env := &workspace.Environment{
		Runtime: workspace.RuntimeVM,
		Nodes:   []workspace.EnvNode{},
		Images:  []string{},
		VMs:     []workspace.EnvVM{},
	}
	multi := config.IsMultiVM(cfg.Runtime.QEMU)
	for _, vm := range cfg.Runtime.QEMU {
		cpus, mem := vm.CPUs, vm.MemoryMB
		// The hypervisor's defaults for a VM that sets neither.
		if cpus <= 0 {
			cpus = 2
		}
		if mem <= 0 {
			mem = 2048
		}
		vmName := vm.Name
		if vmName == "" {
			vmName = name
		}
		env.VMs = append(env.VMs, workspace.EnvVM{Name: vmName, OS: imageOS(vm.Image), CPUs: cpus, MemoryMiB: mem})
	}
	env.Connect = "astrona ssh " + name
	if multi && len(cfg.Runtime.QEMU) > 0 {
		env.Connect = "astrona ssh " + name + "-" + cfg.Runtime.QEMU[0].Name
	}
	return env
}

// imageDistros are the distributions imageOS names from an image's name.
var imageDistros = []struct{ prefix, name string }{
	{"ubuntu", "Ubuntu"}, {"debian", "Debian"}, {"fedora", "Fedora"},
	{"rocky", "Rocky Linux"}, {"almalinux", "AlmaLinux"}, {"alpine", "Alpine Linux"},
}

var leadingVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*`)

// imageOS reads the operating system from a VM image's name
// (ghcr.io/astrona-io/ubuntu-qcow2-image:24.04-base-arm64 → "Ubuntu 24.04",
// noble-ubuntu-24.04.qcow2 → ""): the distribution its last path element
// starts with and the version its tag (or the rest of a file name) starts
// with. "" when the name does not say.
func imageOS(src config.QEMUImageSource) string {
	ref := src.Source
	if src.Type == "file" && src.File != "" {
		ref = src.File
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	ref, _, _ = strings.Cut(ref, "@")
	base := path.Base(strings.ReplaceAll(ref, "\\", "/"))
	repo, tag, hasTag := strings.Cut(base, ":")
	lower := strings.ToLower(repo)
	for _, d := range imageDistros {
		if !strings.HasPrefix(lower, d.prefix) {
			continue
		}
		rest := tag
		if !hasTag {
			rest = strings.TrimLeft(lower[len(d.prefix):], "-_")
		}
		if v := leadingVersion.FindString(rest); v != "" {
			return d.name + " " + v
		}
		return d.name
	}
	return ""
}

// browserSuggestedCommands are the playground's own suggested commands.
func browserSuggestedCommands(cfg *config.LabConfig) []workspace.SuggestedCommand {
	var out []workspace.SuggestedCommand
	for _, c := range cfg.Metadata.SuggestedCommands {
		out = append(out, workspace.SuggestedCommand{Command: strings.TrimSpace(c.Command), Label: strings.TrimSpace(c.Label)})
	}
	return out
}
