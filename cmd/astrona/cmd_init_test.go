package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestScaffoldLabIsValidAsGenerated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "k8s-web-01")
	files, err := scaffoldLab(dir, "k8s-web-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".github/workflows/lab.yml", "config.yaml", "docs/exam-question.md", "solution/web.yaml", "manifests/namespace.yaml", "README.md"} {
		found := false
		for _, f := range files {
			found = found || f == want
		}
		if !found {
			t.Errorf("missing %s in %v", want, files)
		}
	}

	cfg, _, err := config.LoadLabConfig(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metadata.Name != "k8s-web-01" {
		t.Errorf("name = %q", cfg.Metadata.Name)
	}
	for _, r := range labConfigResults(cfg, dir) {
		if r.status != checkOK {
			t.Errorf("generated lab doesn't validate: %s %s %s", r.name, r.detail, r.hint)
		}
	}

	wf, _ := os.ReadFile(filepath.Join(dir, ".github", "workflows", "lab.yml"))
	if !strings.Contains(string(wf), "${{ github.event.pull_request.number }}") {
		t.Error("GitHub Actions expression was mangled by templating")
	}
	q, _ := os.ReadFile(filepath.Join(dir, "docs", "exam-question.md"))
	if !strings.Contains(string(q), "astrona shell k8s-web-01") || strings.Contains(string(q), "[[") {
		t.Errorf("exam question not rendered:\n%s", q)
	}
}

func TestScaffoldLabRefuses(t *testing.T) {
	if _, err := scaffoldLab(t.TempDir(), "Bad_Name"); err == nil {
		t.Error("invalid name accepted")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("mine"), 0600)
	if _, err := scaffoldLab(dir, "lab"); err == nil || !strings.Contains(err.Error(), "isn't empty") {
		t.Errorf("non-empty dir = %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "keep.txt")); string(data) != "mine" {
		t.Error("existing file was touched")
	}
}
