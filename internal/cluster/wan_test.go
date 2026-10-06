package cluster

import (
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestWANCommand(t *testing.T) {
	set := wanCommand("podman", "astro-x-idp-control-plane", config.WANConditions{Latency: "80ms", Loss: "1%"})
	if got := strings.Join(set, " "); got != "podman exec astro-x-idp-control-plane tc qdisc replace dev eth0 root netem delay 80ms loss 1%" {
		t.Errorf("set = %s", got)
	}
	unset := wanCommand("docker", "n", config.WANConditions{})
	if got := strings.Join(unset, " "); got != "docker exec n tc qdisc del dev eth0 root" {
		t.Errorf("clear = %s", got)
	}
	if !noQdisc("Error: Cannot delete qdisc with handle of zero.") {
		t.Error("deleting a missing qdisc must be fine")
	}
	if err := ApplyWAN("astro-x", config.WANConditions{Latency: "1ms; reboot"}); err == nil {
		t.Error("invalid conditions reached tc")
	}
}
