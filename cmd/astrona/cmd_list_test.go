package main

import (
	"bytes"
	"encoding/json"
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
	clusters := groupKindContainers(psOut)
	if len(clusters) != 3 {
		t.Fatalf("clusters = %v, want only the 3 astro- ones", clusters)
	}
	want := map[string]string{
		"astro-a": "Running",
		"astro-b": "Stopped",
		"astro-c": "Degraded (1/2 containers running)",
	}
	for name, status := range want {
		if got := containerStatus(clusters[name]); got != status {
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
		status, version := nodeHealth([]byte(c.json))
		if status != c.status {
			t.Errorf("nodeHealth = %q, want %q", status, c.status)
		}
		if strings.HasPrefix(c.status, "Ready") && version != "v1.37.0" {
			t.Errorf("version = %q", version)
		}
	}
}

func TestPrintLabTableAndJSON(t *testing.T) {
	rows := []labRow{
		{name: "astro-a", runtime: "kind", status: "Ready (2/2)", uptime: "5m", nics: "-", details: "kubectl --context kind-astro-a", version: "v1.37.0", forwards: "1/1 Ready"},
		{name: "astro-vm", runtime: "qemu", status: "Running", uptime: "1h", nics: "1 (mgmt)", details: "ssh …"},
	}

	var buf bytes.Buffer
	printLabTable(&buf, rows, false)
	if strings.Contains(buf.String(), "KUBERNETES") || !strings.Contains(buf.String(), "Ready (2/2)") {
		t.Errorf("default table:\n%s", buf.String())
	}

	buf.Reset()
	printLabTable(&buf, rows, true)
	for _, want := range []string{"KUBERNETES", "FORWARDS", "v1.37.0", "1/1 Ready"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("wide table missing %q:\n%s", want, buf.String())
		}
	}

	buf.Reset()
	if err := printLabJSON(&buf, rows); err != nil {
		t.Fatal(err)
	}
	var got []labJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if got[0].Kubernetes != "v1.37.0" || got[0].PortForwards != "1/1 Ready" || got[0].NICs != "" || got[1].NICs != "1 (mgmt)" {
		t.Errorf("json = %+v", got)
	}

	buf.Reset()
	printLabTable(&buf, nil, false)
	if !strings.Contains(buf.String(), "No astrona labs running.") {
		t.Errorf("empty = %q", buf.String())
	}
}
