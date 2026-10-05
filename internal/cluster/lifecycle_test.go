package cluster

import (
	"errors"
	"testing"
)

func TestParseNodeContainersOrder(t *testing.T) {
	out := `astro-x-worker2|worker|exited
astro-x-control-plane|control-plane|exited
astro-x-worker|worker|running
astro-x-external-load-balancer|external-load-balancer|Exited
bad line
`
	cs := parseNodeContainers(out)
	var got []string
	for _, c := range cs {
		got = append(got, c.Name)
	}
	want := []string{"astro-x-external-load-balancer", "astro-x-control-plane", "astro-x-worker", "astro-x-worker2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("start order = %v, want %v", got, want)
		}
	}
	if cs[0].State != "exited" {
		t.Errorf("state not lowercased: %q", cs[0].State)
	}
}

func TestCheckRestartable(t *testing.T) {
	single := []NodeContainer{{Role: "control-plane"}, {Role: "worker"}, {Role: "worker"}}
	if err := CheckRestartable(single); err != nil {
		t.Fatalf("single control plane refused: %v", err)
	}
	ha := []NodeContainer{{Role: "external-load-balancer"}, {Role: "control-plane"}, {Role: "control-plane"}, {Role: "control-plane"}}
	if err := CheckRestartable(ha); !errors.Is(err, ErrMultiControlPlane) {
		t.Fatalf("HA cluster = %v, want ErrMultiControlPlane", err)
	}
}
