package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/portforward"
)

func sampleForwards() []portforward.Forward {
	pf := config.PortForward{Name: "web", Resource: "svc/frontend", HostPort: 18080, TargetPort: 80, Scheme: "http", Description: "Frontend UI"}
	return []portforward.Forward{{
		Spec: portforward.Spec{Lab: "astro-demo", KubeContext: "kind-astro-demo", Forward: pf.Normalized(), StartedAt: time.Now()},
		// PID 0: no supervisor, so Effective is Stopped without touching the network.
		Status: portforward.Status{State: portforward.StateReady, Restarts: 2, LastError: "lost connection to pod"},
	}}
}

func TestPrintPortForwardTable(t *testing.T) {
	var buf bytes.Buffer
	printPortForwardTable(&buf, nil, false)
	if !strings.Contains(buf.String(), "No port forwards found.") {
		t.Fatalf("empty table: %q", buf.String())
	}

	buf.Reset()
	printPortForwardTable(&buf, sampleForwards(), false)
	out := buf.String()
	for _, want := range []string{"NAME", "STATUS", "web", "astro-demo", "Stopped", "http://127.0.0.1:18080", "svc/frontend:80"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "LAST ERROR") {
		t.Error("narrow table should not include LAST ERROR")
	}

	buf.Reset()
	printPortForwardTable(&buf, sampleForwards(), true)
	if out := buf.String(); !strings.Contains(out, "LAST ERROR") || !strings.Contains(out, "lost connection to pod") {
		t.Errorf("wide table missing last error:\n%s", out)
	}
}

func TestPrintPortForwardHints(t *testing.T) {
	var buf bytes.Buffer
	printPortForwardHints(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("hints for no forwards should print nothing, got %q", buf.String())
	}

	printPortForwardHints(&buf, sampleForwards())
	out := buf.String()
	for _, want := range []string{"bound to 127.0.0.1 only", "http://127.0.0.1:18080", "svc/frontend:80 (ns default)", "Frontend UI", "Not ready yet", "lost connection to pod", "astrona port-forward list"} {
		if !strings.Contains(out, want) {
			t.Errorf("hints missing %q:\n%s", want, out)
		}
	}
}
