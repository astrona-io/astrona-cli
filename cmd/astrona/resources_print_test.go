package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"astrona/internal/config"
	"astrona/internal/resources"
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

// hasControl reports whether s holds anything a terminal could act on:
// a control character (newline and tab included) or an invalid UTF-8 byte.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == utf8.RuneError })
}

func TestResourceListingsSanitized(t *testing.T) {
	evil := "x\x1b[1A\x1b[2K\r\n  • fake\x9b"
	lab := writeLabFiles(t, map[string]string{
		"config.yaml":              "",
		"resources/demo.go":        "package main",
		"resources/notes.\x1b[2Jt": "x",
	})
	cfg := &config.LabConfig{Resources: []config.LabResource{
		{File: "demo.go", Run: "go run ." + evil, Description: "Demo" + evil},
		{File: "notes.\x1b[2Jt", Type: config.ResourceTypeFile},
	}}
	list, err := resources.Collect(cfg, lab)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	printResources(&b, "/tmp/res", list)
	printDocResources(&b, cfg, filepath.Join(lab, "config.yaml"))
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if hasControl(line) {
			t.Errorf("listing line has control characters: %q", line)
		}
		if strings.HasPrefix(strings.TrimSpace(line), "• fake") {
			t.Errorf("a lab string broke onto its own line: %q", line)
		}
	}
	for _, l := range resourceRiskLines(cfg, lab) {
		if hasControl(l) {
			t.Errorf("trust line has control characters: %q", l)
		}
	}
	for _, c := range resourceCompletions(list) {
		name, desc, _ := strings.Cut(string(c), "\t")
		if hasControl(name) || hasControl(desc) {
			t.Errorf("completion has control characters: %q", c)
		}
	}
}
