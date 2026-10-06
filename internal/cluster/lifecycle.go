package cluster

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"astrona/internal/ui"
)

// NodeContainer is one container of a kind cluster.
type NodeContainer struct {
	Name, Role, State string
}

// ErrMultiControlPlane: kind can't restart a cluster with more than one
// control plane — the node containers come back with new IPs, and etcd's
// peer URLs/certificates are bound to the old ones, so the API never
// recovers (verified on podman). Single-control-plane clusters are fine:
// workers reach the control plane by name.
var ErrMultiControlPlane = errors.New("clusters with more than one control plane can't be stopped and restarted (node IPs change on restart, which breaks etcd) — use `astrona destroy` and `astrona run` instead")

// rolePriority is the start order: the HA load balancer first, then the
// control plane(s), then workers.
var rolePriority = map[string]int{"external-load-balancer": 0, "control-plane": 1, "worker": 2}

// KindNodeContainers lists clusterName's containers (running or not).
func KindNodeContainers(clusterName string) ([]NodeContainer, ContainerEngine, error) {
	engine, err := DetectContainerEngine()
	if err != nil {
		return nil, engine, err
	}
	out, err := exec.Command(engine.Path, "ps", "-a",
		"--filter", "label=io.x-k8s.kind.cluster="+clusterName,
		"--format", `{{.Names}}|{{.Label "io.x-k8s.kind.role"}}|{{.State}}`).Output()
	if err != nil {
		return nil, engine, fmt.Errorf("%s ps failed: %w", engine.Name, err)
	}
	cs := parseNodeContainers(string(out))
	if len(cs) == 0 {
		return nil, engine, fmt.Errorf("no containers found for kind cluster '%s'", clusterName)
	}
	return cs, engine, nil
}

func parseNodeContainers(out string) []NodeContainer {
	var cs []NodeContainer
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) == 3 && parts[0] != "" {
			cs = append(cs, NodeContainer{Name: parts[0], Role: parts[1], State: strings.ToLower(parts[2])})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if rolePriority[cs[i].Role] != rolePriority[cs[j].Role] {
			return rolePriority[cs[i].Role] < rolePriority[cs[j].Role]
		}
		return cs[i].Name < cs[j].Name
	})
	return cs
}

// CheckRestartable returns ErrMultiControlPlane for an HA cluster.
func CheckRestartable(cs []NodeContainer) error {
	cp := 0
	for _, c := range cs {
		if c.Role == "control-plane" {
			cp++
		}
	}
	if cp > 1 {
		return ErrMultiControlPlane
	}
	return nil
}

func names(cs []NodeContainer) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

// StopKindCluster stops every node container of clusterName. Nothing is
// deleted — StartKindCluster brings it back as it was.
func StopKindCluster(clusterName string, rep *ui.Reporter) error {
	t := rep.Step("Stop kind cluster %q", clusterName)
	cs, engine, err := KindNodeContainers(clusterName)
	if err != nil {
		return t.Fail(err)
	}
	if err := CheckRestartable(cs); err != nil {
		return t.Fail(err)
	}
	cmd := exec.Command(engine.Path, append([]string{"stop"}, names(cs)...)...)
	cmd.Stdout, cmd.Stderr = t.Output(), t.Output()
	if err := cmd.Run(); err != nil {
		return t.Fail(fmt.Errorf("%s stop failed: %w", engine.Name, err))
	}
	t.Done()
	return nil
}

// StartKindCluster starts clusterName's node containers, control plane
// before workers.
func StartKindCluster(clusterName string, rep *ui.Reporter) error {
	t := rep.Step("Start kind cluster %q", clusterName)
	cs, engine, err := KindNodeContainers(clusterName)
	if err != nil {
		return t.Fail(err)
	}
	if err := CheckRestartable(cs); err != nil {
		return t.Fail(err)
	}
	cmd := exec.Command(engine.Path, append([]string{"start"}, names(cs)...)...)
	cmd.Stdout, cmd.Stderr = t.Output(), t.Output()
	if err := cmd.Run(); err != nil {
		return t.Fail(fmt.Errorf("%s start failed: %w", engine.Name, err))
	}
	t.Done()
	return nil
}

// Exists reports whether kind cluster name exists (running or stopped):
// a container with kind's cluster label named <name>-control-plane.
func Exists(name string) bool {
	engine, err := DetectContainerEngine()
	if err != nil {
		return false
	}
	out, err := exec.Command(engine.Path, "ps", "-a",
		"--filter", "label=io.x-k8s.kind.cluster",
		"--format", "{{.Names}}").Output()
	if err != nil {
		return false
	}
	target := name + "-control-plane"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}
