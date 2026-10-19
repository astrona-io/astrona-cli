package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"time"
)

// podmanNetwork is the network kind puts its nodes on with Podman.
func podmanNetwork() string {
	if name := os.Getenv("KIND_EXPERIMENTAL_PODMAN_NETWORK"); name != "" {
		return name
	}
	return "kind"
}

// SetNetworkDNS points the Podman network kind uses at servers: its DNS
// (aardvark) still answers node and container names, and forwards every
// other lookup — the kubelet's image pulls and CoreDNS's upstream — to
// servers instead of the host's resolver. Docker networks have no such
// setting, so with Docker this does nothing. The network is shared by every
// kind cluster on the machine; the last lab created sets it.
func SetNetworkDNS(engine ContainerEngine, servers []string, out io.Writer) error {
	if engine.Name != "podman" || len(servers) == 0 {
		return nil
	}
	network := podmanNetwork()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, engine.Path, "network", "inspect", network, "--format", "{{json .NetworkDNSServers}}").Output()
	if err != nil {
		return fmt.Errorf("read the DNS servers of podman network %s: %w", network, err)
	}
	var current []string
	if err := json.Unmarshal(data, &current); err != nil {
		return fmt.Errorf("read the DNS servers of podman network %s: %w", network, err)
	}
	if slices.Equal(current, servers) {
		return nil
	}
	args := []string{"network", "update"}
	for _, server := range current {
		args = append(args, "--dns-drop", server)
	}
	for _, server := range servers {
		args = append(args, "--dns-add", server)
	}
	args = append(args, network)
	cmd := exec.CommandContext(ctx, engine.Path, args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("set the DNS servers of podman network %s: %w", network, err)
	}
	fmt.Fprintf(out, "podman network %s now forwards DNS to %v\n", network, servers)
	return nil
}
