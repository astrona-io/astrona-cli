package labops

import (
	"strings"
	"testing"
)

const psOut = `astro-a-control-plane|running|astro-a|control-plane
astro-a-worker|running|astro-a|worker
astro-b-control-plane|exited|astro-b|control-plane
astro-c-control-plane|running|astro-c|control-plane
astro-c-worker|exited|astro-c|worker
kind-control-plane|running|kind|control-plane
broken line
`

func TestGroupKindContainersAndStatus(t *testing.T) {
	clusters := GroupKindContainers(psOut)
	if len(clusters) != 3 {
		t.Fatalf("clusters = %v, want only the 3 astro- ones", clusters)
	}
	want := map[string]string{
		"astro-a": "Running",
		"astro-b": "Stopped",
		"astro-c": "Degraded (1/2 containers running)",
	}
	for name, status := range want {
		if got := ContainerStatus(clusters[name]); got != status {
			t.Errorf("%s: status = %q, want %q", name, got, status)
		}
	}
}

func TestNodeHealth(t *testing.T) {
	node := func(ready string) string {
		return `{"status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"` + ready + `"}],"nodeInfo":{"kubeletVersion":"v1.37.0"}}}`
	}
	cases := []struct {
		json, status string
	}{
		{`{"items":[` + node("True") + `,` + node("True") + `]}`, "Ready (2/2)"},
		{`{"items":[` + node("True") + `,` + node("False") + `]}`, "NotReady (1/2)"},
		{`{"items":[]}`, "Unreachable"},
		{`not json`, "Unreachable"},
	}
	for _, c := range cases {
		status, version := NodeHealth([]byte(c.json))
		if status != c.status {
			t.Errorf("NodeHealth = %q, want %q", status, c.status)
		}
		if strings.HasPrefix(c.status, "Ready") && version != "v1.37.0" {
			t.Errorf("version = %q", version)
		}
	}
}

func TestMarkLinkedClusters(t *testing.T) {
	rows := []LabRow{{Name: "astro-app", Details: "kubectl --context kind-astro-app"}, {Name: "astro-app-idp", Details: "kubectl --context kind-astro-app-idp"}}
	MarkLinkedClusters(rows, map[string]string{"astro-app-idp": "astro-app"})
	if strings.Contains(rows[0].Details, "linked") || !strings.HasPrefix(rows[1].Details, "linked cluster of astro-app · kubectl") {
		t.Fatalf("rows = %+v", rows)
	}
}
