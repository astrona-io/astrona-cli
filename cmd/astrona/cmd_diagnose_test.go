package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/diagnostics"
)

func TestWantDiagnostics(t *testing.T) {
	fail := errors.New("boom")
	cases := []struct {
		mode string
		err  error
		want bool
	}{
		{"on-failure", fail, true},
		{"on-failure", nil, false},
		{"always", nil, true},
		{"never", fail, false},
	}
	for _, c := range cases {
		if got := wantDiagnostics(c.mode, c.err); got != c.want {
			t.Errorf("wantDiagnostics(%s, %v) = %v", c.mode, c.err, got)
		}
	}
	if validateDiagnosticsMode("sometimes") == nil {
		t.Error("invalid mode accepted")
	}
}

func TestQEMUConsoleLogNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	single := qemuConsoleLogs(&config.LabConfig{Runtime: config.RuntimeConfig{QEMU: []config.QEMUVM{{}}}}, "astro-x")
	if len(single) != 1 || single[0].Name != "astro-x" || !strings.HasSuffix(single[0].ConsoleLog, "/astro-x/console.log") {
		t.Errorf("single VM = %+v", single)
	}
	multi := qemuConsoleLogs(&config.LabConfig{Runtime: config.RuntimeConfig{QEMU: []config.QEMUVM{{Name: "a"}, {Name: "b"}}}}, "astro-x")
	if len(multi) != 2 || multi[0].Name != "astro-x-a" || multi[1].Name != "astro-x-b" {
		t.Errorf("multi VM = %+v", multi)
	}
}

func TestPrintDiagnosticsSummary(t *testing.T) {
	var buf bytes.Buffer
	var probs []string
	for i := range 12 {
		probs = append(probs, "ns/pod-"+string(rune('a'+i))+": CrashLoopBackOff")
	}
	printDiagnosticsSummary(&buf, diagnostics.Summary{Dir: "/tmp/d", Problems: probs, Warnings: 3})
	out := buf.String()
	for _, want := range []string{"Diagnostics bundle: /tmp/d", "Unhealthy pods (12)", "ns/pod-a: CrashLoopBackOff", "… 2 more in summary.md", "3 warning event(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pod-l") {
		t.Error("printed more than 10 problem lines")
	}
}
