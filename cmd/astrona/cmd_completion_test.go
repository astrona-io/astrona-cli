package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/cluster"

	"github.com/spf13/cobra"
)

func names(cs []cobra.Completion) []string {
	var out []string
	for _, c := range cs {
		out = append(out, strings.SplitN(c, "\t", 2)[0])
	}
	return out
}

func TestLabNameCandidates(t *testing.T) {
	rows := []labRow{
		{name: "astro-web", runtime: "kind", status: "Ready (1/1)"},
		{name: "astro-db", runtime: "kind", status: "Stopped"},
		{name: "astro-vm", runtime: "qemu", status: "Running"},
	}
	cases := []struct {
		keep       func(labRow) bool
		toComplete string
		want       string
	}{
		{nil, "", "astro-web,astro-db,astro-vm"},
		{nil, "astro-w", "astro-web"},
		{nil, "ast", "astro-web,astro-db,astro-vm"}, // still typing the prefix
		{nil, "w", "web"},                           // unprefixed form once it can't be the prefix
		{isKind, "", "astro-web,astro-db"},
		{isQEMU, "", "astro-vm"},
		{isStoppedKind, "", "astro-db"},
		{func(r labRow) bool { return isKind(r) && isRunning(r) }, "", "astro-web"},
		{nil, "zzz", ""},
	}
	for _, c := range cases {
		got := strings.Join(names(labNameCandidates(rows, c.keep, c.toComplete)), ",")
		if got != c.want {
			t.Errorf("toComplete=%q: got %q, want %q", c.toComplete, got, c.want)
		}
	}
	if desc := labNameCandidates(rows, nil, "astro-db")[0]; !strings.Contains(desc, "\tkind, Stopped") {
		t.Errorf("completion description = %q", desc)
	}
}

func complete(t *testing.T, args ...string) string {
	t.Helper()
	root := newRootCmd(&rootFlags{})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&strings.Builder{})
	root.SetArgs(append([]string{"__complete"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestClusterAndVersionCompletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	// --cluster from the lab config (reset takes a config, not a name).
	lab := t.TempDir()
	os.WriteFile(filepath.Join(lab, "config.yaml"), []byte("metadata: {name: c}\nruntime:\n  kind:\n    clusters: [{name: idp}, {name: db}]\n"), 0600)
	if got := complete(t, "reset", "-c", lab, "--cluster", ""); !strings.Contains(got, "idp\tlinked cluster") || !strings.Contains(got, "db\tlinked cluster") {
		t.Errorf("reset --cluster completion:\n%s", got)
	}
	if got := complete(t, "reset", "-c", lab, "--cluster", "i"); strings.Contains(got, "db") {
		t.Errorf("prefix not applied:\n%s", got)
	}

	// --cluster from a running lab's saved state (shell takes a name).
	cluster.WriteLinks("astro-app", []cluster.LinkState{{Name: "backend", Cluster: "astro-app-backend"}})
	if got := complete(t, "shell", "app", "--cluster", ""); !strings.Contains(got, "backend\tlinked cluster") {
		t.Errorf("shell --cluster completion:\n%s", got)
	}

	// versions remove: installed ones; install: releases not installed.
	bin := filepath.Join(home, ".astrona", "bin")
	os.MkdirAll(bin, 0755)
	os.WriteFile(filepath.Join(bin, "astrona-0.2.1"), []byte("#!/bin/sh\n"), 0755)
	if got := complete(t, "versions", "remove", ""); !strings.Contains(got, "0.2.1\tinstalled") {
		t.Errorf("versions remove completion:\n%s", got)
	}
	quickReleaseTags = func() ([]string, error) { return []string{"v0.2.2", "v0.2.1", "v0.2.0"}, nil }
	defer func() { quickReleaseTags = func() ([]string, error) { return listReleaseTags(3 * time.Second) } }()
	got := complete(t, "versions", "install", "")
	if !strings.Contains(got, "0.2.2\trelease") || !strings.Contains(got, "0.2.0\trelease") || strings.Contains(got, "0.2.1\t") {
		t.Errorf("versions install completion (0.2.1 is installed):\n%s", got)
	}
}
