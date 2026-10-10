package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/resources"

	"github.com/spf13/cobra"
)

// snapshotLab gives lab a copy of resources made from files (path →
// content, "*" suffix = executable) and returns the copy's folder.
func snapshotLab(t *testing.T, lab string, files map[string]string, entries ...config.LabResource) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, "*") {
			name, mode = strings.TrimSuffix(name, "*"), 0o755
		}
		p := filepath.Join(dir, "resources", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	list, err := resources.Collect(&config.LabConfig{Resources: entries}, dir)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := resources.Snapshot(lab, dir, list)
	if err != nil {
		t.Fatal(err)
	}
	return dest
}

func resourceTestCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("config", ".", "")
	cmd.Flags().String("git", "", "")
	return cmd
}

func TestPickResourceLab(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(labShellEnvVar, "")
	flags := &rootFlags{configPath: ".", fileName: "config.yaml"}

	if _, err := pickResourceLab(resourceTestCmd(), flags, ""); err == nil || !strings.Contains(err.Error(), "no lab has resources") {
		t.Errorf("none = %v", err)
	}
	snapshotLab(t, "astro-net-01", map[string]string{"a.sh": ""})
	if lab, err := pickResourceLab(resourceTestCmd(), flags, ""); err != nil || lab != "astro-net-01" {
		t.Errorf("the only one = %q, %v", lab, err)
	}
	snapshotLab(t, "astro-db-02", map[string]string{"b.sh": ""})
	if _, err := pickResourceLab(resourceTestCmd(), flags, ""); err == nil || !strings.Contains(err.Error(), "db-02, net-01") || !strings.Contains(err.Error(), "astrona shell <lab>") {
		t.Errorf("several = %v", err)
	}
	// The terminal's own lab (astrona shell) wins over guessing…
	t.Setenv(labShellEnvVar, "astro-db-02")
	if lab, _ := pickResourceLab(resourceTestCmd(), flags, ""); lab != "astro-db-02" {
		t.Errorf("$ASTRONA_LAB = %q", lab)
	}
	// …and --lab over everything.
	if lab, _ := pickResourceLab(resourceTestCmd(), flags, "net-01"); lab != "astro-net-01" {
		t.Errorf("--lab = %q", lab)
	}
}

func TestResourceCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := snapshotLab(t, "astro-lab", map[string]string{
		"setup.sh":       "echo hi",
		"app.yaml":       "kind: Pod",
		"check*":         "#!/bin/sh\necho ok",
		"notes.txt":      "x",
		"tool/main.go":   "package main",
		"single/run.txt": "x",
	}, config.LabResource{File: "tool", Run: "go run ."})
	list, _, err := resources.Load("astro-lab")
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	for name, want := range map[string]struct {
		cmd  string
		args string
		in   string
	}{
		"setup": {"bash", filepath.Join(dir, "setup.sh") + " --x", wd},
		"app":   {"kubectl", "apply -f " + filepath.Join(dir, "app.yaml") + " --x", wd},
		"check": {filepath.Join(dir, "check"), "--x", wd},
		"tool":  {"go", "run . --x", filepath.Join(dir, "tool")},
	} {
		r, ok := resources.Find(list, name)
		if !ok {
			t.Fatalf("no %s in %+v", name, list)
		}
		cmd, argv, in, err := resourceCommand(dir, r, []string{"--x"})
		if err != nil || cmd != want.cmd || strings.Join(argv, " ") != want.args || in != want.in {
			t.Errorf("%s = %q %q in %q, %v; want %q %q in %q", name, cmd, argv, in, err, want.cmd, want.args, want.in)
		}
	}
	notes, _ := resources.Find(list, "NOTES") // case-insensitive
	if _, _, _, err := resourceCommand(dir, notes, nil); err == nil || !strings.Contains(err.Error(), "astrona res copy notes") {
		t.Errorf("a file = %v", err)
	}
}

func TestResourceCopyTo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := snapshotLab(t, "astro-lab", map[string]string{"app.yaml": "kind: Pod", "tool/main.go": "package main", "run.sh*": "#!/bin/sh"})
	list, _, _ := resources.Load("astro-lab")
	out := t.TempDir()

	app, _ := resources.Find(list, "app")
	got, err := app.CopyTo(dir, out, false)
	if err != nil || got != filepath.Join(out, "app.yaml") {
		t.Fatalf("copy into a folder = %q, %v", got, err)
	}
	if b, _ := os.ReadFile(got); string(b) != "kind: Pod" {
		t.Errorf("content = %q", b)
	}
	if _, err := app.CopyTo(dir, out, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("existing file = %v", err)
	}
	if _, err := app.CopyTo(dir, out, true); err != nil {
		t.Errorf("--force = %v", err)
	}
	tool, _ := resources.Find(list, "tool")
	if got, err := tool.CopyTo(dir, filepath.Join(out, "mytool"), false); err != nil || got != filepath.Join(out, "mytool") {
		t.Errorf("folder to a new name = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, "mytool", "main.go")); err != nil {
		t.Error(err)
	}
	run, _ := resources.Find(list, "run")
	got, _ = run.CopyTo(dir, out, false)
	if fi, _ := os.Stat(got); fi.Mode().Perm() != 0o755 {
		t.Errorf("executable copied as %v", fi.Mode().Perm())
	}
}
