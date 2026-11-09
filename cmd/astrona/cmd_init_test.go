package main

import (
	"path/filepath"
	"testing"

	"astrona/internal/authoring"
	"astrona/internal/config"
	"astrona/internal/preflight"
)

// The starters themselves are tested in internal/authoring; validate
// still lives here, so the "passes astrona validate as generated" half
// stays with it.

func TestScaffoldLabIsValidAsGenerated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "k8s-web-01")
	if _, err := authoring.Scaffold(dir, "k8s-web-01"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadLabConfig(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range authoring.CheckConfig(cfg, dir) {
		if r.Status != preflight.OK {
			t.Errorf("generated lab doesn't validate: %s %s %s", r.Name, r.Detail, r.Hint)
		}
	}
}

func TestScaffoldLinkedLabIsValidAsGenerated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth-01")
	if _, err := authoring.ScaffoldLinked(dir, "auth-01"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadLabConfig(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range authoring.CheckConfig(cfg, dir) {
		if r.Status != preflight.OK {
			t.Errorf("generated lab doesn't validate: %s %s %s", r.Name, r.Detail, r.Hint)
		}
	}
}
