package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrintLabTableAndJSON(t *testing.T) {
	rows := []labRow{
		{Name: "astro-a", Runtime: "kind", Status: "Ready (2/2)", Uptime: "5m", NICs: "-", Details: "kubectl --context kind-astro-a", Version: "v1.37.0", Forwards: "1/1 Ready"},
		{Name: "astro-vm", Runtime: "qemu", Status: "Running", Uptime: "1h", NICs: "1 (mgmt)", Details: "ssh …"},
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
