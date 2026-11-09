package labops

import (
	"bytes"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/diagnostics"
)

func TestQEMUConsoleLogNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	single := QEMUConsoleLogs(&config.LabConfig{Runtime: config.RuntimeConfig{QEMU: []config.QEMUVM{{}}}}, "astro-x")
	if len(single) != 1 || single[0].Name != "astro-x" || !strings.HasSuffix(single[0].ConsoleLog, "/astro-x/console.log") {
		t.Errorf("single VM = %+v", single)
	}
	multi := QEMUConsoleLogs(&config.LabConfig{Runtime: config.RuntimeConfig{QEMU: []config.QEMUVM{{Name: "a"}, {Name: "b"}}}}, "astro-x")
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
	WriteDiagnosticsSummary(&buf, diagnostics.Summary{Dir: "/tmp/d", Problems: probs, Warnings: 3})
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

func TestPrintLinkedDiagnostics(t *testing.T) {
	var out strings.Builder
	WriteLinkedDiagnostics(&out, []LinkedDiagnostics{
		{Cluster: "astro-app-idp", Summary: diagnostics.Summary{Dir: "/d/clusters/astro-app-idp", Problems: []string{"auth/idp-1: CrashLoopBackOff"}, Warnings: 3}},
		{Cluster: "astro-app-db", Summary: diagnostics.Summary{Dir: "/d/clusters/astro-app-db", Warnings: 1}},
	})
	got := out.String()
	for _, want := range []string{"Linked cluster astro-app-idp: /d/clusters/astro-app-idp", "auth/idp-1: CrashLoopBackOff", "Linked cluster astro-app-db", "No unhealthy pods. 1 warning event(s)."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
