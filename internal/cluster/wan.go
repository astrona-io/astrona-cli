package cluster

import (
	"fmt"
	"os/exec"
	"strings"

	"astrona/internal/config"
)

// ApplyWAN sets w on the network interface of every node of kind cluster
// clusterName (`tc qdisc replace … netem …` — idempotent), or clears it
// when w is zero. Arguments go to the engine's exec as a slice, never
// through a shell; w must have passed Validate.
func ApplyWAN(clusterName string, w config.WANConditions) error {
	if err := w.Validate(); err != nil {
		return err
	}
	nodes, engine, err := KindNodeContainers(clusterName)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.Role != "control-plane" && n.Role != "worker" {
			continue // the HA load balancer isn't a node
		}
		args := wanCommand(engine.Path, n.Name, w)
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		// Clearing a node that has no conditions is fine.
		if err != nil && (!w.IsZero() || !noQdisc(string(out))) {
			return fmt.Errorf("tc on %s: %w: %s", n.Name, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// wanCommand is the engine command setting (or clearing) w on node.
func wanCommand(engine, node string, w config.WANConditions) []string {
	base := []string{engine, "exec", node, "tc", "qdisc"}
	if w.IsZero() {
		return append(base, "del", "dev", "eth0", "root")
	}
	return append(append(base, "replace", "dev", "eth0", "root"), w.NetemArgs()...)
}

// noQdisc recognizes tc's answer to deleting a qdisc that isn't there.
func noQdisc(out string) bool {
	return strings.Contains(out, "Cannot delete qdisc with handle of zero") || strings.Contains(out, "No such file or directory")
}
