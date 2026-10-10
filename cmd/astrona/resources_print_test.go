package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestResourceRiskLines(t *testing.T) {
	dir := t.TempDir()
	if got := resourceRiskLines(&config.LabConfig{}, dir); got != nil {
		t.Errorf("no resources = %v", got)
	}
	lab := writeLabFiles(t, map[string]string{"resources/setup.sh": "echo", "resources/app.yaml": "kind: Pod", "resources/notes.txt": "x"})
	got := strings.Join(resourceRiskLines(&config.LabConfig{}, lab), "\n")
	for _, want := range []string{"ships 3 resource(s)", "on this machine only when you ask", "setup (bash setup.sh)", "app (kubectl apply -f app.yaml)", "notes (file, never run)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if got := strings.Join(resourceRiskLines(&config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu"}}, lab), ""); !strings.Contains(got, "inside the lab VM") {
		t.Errorf("qemu = %s", got)
	}
	bad := writeLabFiles(t, map[string]string{"resources/a.sh": "", "resources/a.yaml": ""})
	if got := strings.Join(resourceRiskLines(&config.LabConfig{}, bad), ""); !strings.Contains(got, "can't be read") {
		t.Errorf("broken = %s", got)
	}
}

// writeLabFiles writes files (path → content) under a temp lab folder.
func writeLabFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPrintDocResources(t *testing.T) {
	lab := writeLabFiles(t, map[string]string{"resources/setup.sh": "echo", "config.yaml": ""})
	cfg := &config.LabConfig{Resources: []config.LabResource{{File: "setup.sh", Description: "Creates the demo DB"}}}
	var b strings.Builder
	printDocResources(&b, cfg, filepath.Join(lab, "config.yaml"))
	if !strings.Contains(b.String(), "astrona res show|copy|run") || !strings.Contains(b.String(), "Creates the demo DB") {
		t.Errorf("got:\n%s", b.String())
	}
	b.Reset()
	printDocResources(&b, cfg, "https://example.com/lab/config.yaml")
	if b.Len() != 0 {
		t.Errorf("URL config printed:\n%s", b.String())
	}
}
