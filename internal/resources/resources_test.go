package resources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

// lab writes files (path → content; a trailing "*" on the path marks it
// executable) under a temp lab folder and returns it.
func lab(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, "*") {
			name, mode = strings.TrimSuffix(name, "*"), 0o755
		}
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func byName(list []Resource) map[string]Resource {
	m := map[string]Resource{}
	for _, r := range list {
		m[r.Name] = r
	}
	return m
}

func TestCollect(t *testing.T) {
	dir := lab(t, map[string]string{
		"resources/setup-db.sh":           "echo db",
		"resources/app.yaml":              "kind: Deployment",
		"resources/broken-deployment.yml": "kind: Deployment",
		"resources/check*":                "#!/bin/sh\necho ok",
		"resources/notes.txt":             "hello",
		"resources/load-test/main.go":     "package main",
		"resources/.DS_Store":             "",
	})
	cfg := &config.LabConfig{Resources: []config.LabResource{
		{File: "broken-deployment.yml", Description: "The Deployment you'll fix", Type: "file"},
		{File: "load-test", Description: "Generates traffic", Run: "go run ."},
	}}
	list, err := Collect(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := byName(list)
	for name, want := range map[string]string{
		"setup-db":          HowBash,
		"app":               HowApply,
		"broken-deployment": HowFile,
		"check":             HowExec,
		"notes":             HowFile,
		"load-test":         HowCommand,
	} {
		if got[name].How != want {
			t.Errorf("%s: how = %q, want %q", name, got[name].How, want)
		}
	}
	if len(list) != 6 {
		t.Errorf("got %d resources (hidden files must be skipped): %+v", len(list), list)
	}
	if r := got["load-test"]; r.Command() != "go run ." || !r.Dir || r.Description != "Generates traffic" {
		t.Errorf("load-test = %+v", r)
	}
	if got["setup-db"].Command() != "bash setup-db.sh" || got["app"].Command() != "kubectl apply -f app.yaml" {
		t.Errorf("commands: %q, %q", got["setup-db"].Command(), got["app"].Command())
	}

	// A qemu lab has no cluster to apply YAML to.
	q, _ := Collect(&config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu"}}, dir)
	if byName(q)["app"].How != HowFile {
		t.Errorf("qemu yaml = %q, want file", byName(q)["app"].How)
	}
}

func TestCollectNone(t *testing.T) {
	if list, err := Collect(&config.LabConfig{}, t.TempDir()); err != nil || list != nil {
		t.Errorf("no resources/ = %v, %v", list, err)
	}
	if list, err := Collect(&config.LabConfig{}, ""); err != nil || list != nil {
		t.Errorf("URL config = %v, %v", list, err)
	}
	cfg := &config.LabConfig{Resources: []config.LabResource{{File: "x.sh"}}}
	if _, err := Collect(cfg, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no resources/ folder") {
		t.Errorf("entries without the folder = %v", err)
	}
	if _, err := Collect(cfg, ""); err == nil || !strings.Contains(err.Error(), "lab's folder") {
		t.Errorf("entries on a URL config = %v", err)
	}
}

func TestCollectRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		files map[string]string
		cfg   []config.LabResource
		link  string // resources/<link> → outside the lab
		want  string
	}{
		"missing entry":   {files: map[string]string{"resources/a.sh": ""}, cfg: []config.LabResource{{File: "b.sh"}}, want: "doesn't exist"},
		"escaping entry":  {files: map[string]string{"resources/a.sh": "", "secret": ""}, cfg: []config.LabResource{{File: "../secret"}}, want: "escapes"},
		"absolute entry":  {files: map[string]string{"resources/a.sh": ""}, cfg: []config.LabResource{{File: "/etc/passwd"}}, want: "relative"},
		"same name":       {files: map[string]string{"resources/setup.sh": "", "resources/setup.yaml": ""}, want: "both called"},
		"bad name":        {files: map[string]string{"resources/my setup.sh": ""}, want: "rename it"},
		"link to outside": {files: map[string]string{"resources/a.sh": ""}, link: "evil", want: "is a link"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := lab(t, c.files)
			if c.link != "" {
				if err := os.Symlink("/etc/hosts", filepath.Join(dir, "resources", c.link)); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Collect(&config.LabConfig{Resources: c.cfg}, dir)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestSnapshotAndRemove(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := lab(t, map[string]string{
		"resources/setup.sh*":        "#!/bin/bash\necho hi",
		"resources/app.yaml":         "kind: Pod",
		"resources/tool/main.go":     "package main",
		"resources/tool/data/in.txt": "x",
	})
	cfg := &config.LabConfig{Resources: []config.LabResource{{File: "tool", Run: "go run .", Description: "A tool"}}}
	list, err := Collect(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := Snapshot("astro-lab-01", dir, list)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Dir("astro-lab-01")
	if dest != want || !strings.HasSuffix(dest, filepath.Join(".astrona", "resources", "astro-lab-01")) {
		t.Errorf("dest = %s", dest)
	}
	for file, mode := range map[string]os.FileMode{"setup.sh": 0o700, "app.yaml": 0o600, "tool/data/in.txt": 0o600} {
		fi, err := os.Stat(filepath.Join(dest, file))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s: mode %v, want %v", file, fi.Mode().Perm(), mode)
		}
	}
	if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v, want 0700", fi.Mode().Perm())
	}
	var m []Resource
	b, err := os.ReadFile(filepath.Join(dest, manifestFile))
	if err != nil || json.Unmarshal(b, &m) != nil || len(m) != 3 || byName(m)["tool"].Run != "go run ." {
		t.Errorf("manifest = %s, %v", b, err)
	}

	// A new snapshot replaces the old one entirely.
	if err := os.Remove(filepath.Join(dir, "resources", "app.yaml")); err != nil {
		t.Fatal(err)
	}
	list, _ = Collect(cfg, dir)
	if _, err := Snapshot("astro-lab-01", dir, list); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "app.yaml")); err == nil {
		t.Error("a removed resource survived the next snapshot")
	}

	// No resources: the copy goes.
	if d, err := Snapshot("astro-lab-01", dir, nil); err != nil || d != "" {
		t.Errorf("empty snapshot = %q, %v", d, err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("copy survived an empty snapshot")
	}
	if err := Remove("astro-lab-01"); err != nil {
		t.Errorf("removing what isn't there = %v", err)
	}
	if _, err := Dir("../escape"); err == nil {
		t.Error("Dir accepted an escaping lab name")
	}
}

func TestSnapshotRefusesNestedLink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := lab(t, map[string]string{"resources/tool/main.go": "package main"})
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "resources", "tool", "hosts")); err != nil {
		t.Fatal(err)
	}
	list, err := Collect(&config.LabConfig{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot("astro-lab-01", dir, list); err == nil || !strings.Contains(err.Error(), "is a link") {
		t.Errorf("nested link = %v", err)
	}
	if d, _ := Dir("astro-lab-01"); func() bool { _, err := os.Stat(d); return err == nil }() {
		t.Error("a failed snapshot left a copy behind")
	}
}
