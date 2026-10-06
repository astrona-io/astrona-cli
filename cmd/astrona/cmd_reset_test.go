package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestConfirmReset(t *testing.T) {
	cases := map[string]bool{"y\n": true, "YES\n": true, " yes \n": true, "n\n": false, "\n": false, "": false, "sure\n": false}
	for in, want := range cases {
		var out bytes.Buffer
		if got := confirmReset(strings.NewReader(in), &out, "astro-x"); got != want {
			t.Errorf("confirmReset(%q) = %v, want %v", in, got, want)
		}
		if !strings.Contains(out.String(), "Reset astro-x?") {
			t.Errorf("prompt = %q", out.String())
		}
	}
}

func TestLabExistsQEMU(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mark := func(name string) {
		dir := filepath.Join(home, ".astrona", "qemu", name)
		os.MkdirAll(dir, 0700)
		os.WriteFile(filepath.Join(dir, "handle.json"), []byte("{}"), 0600)
	}

	multi := &config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{Name: "a"}, {Name: "b"}}}}
	if labExists(multi, "astro-net") {
		t.Fatal("nothing exists yet")
	}
	mark("astro-net-b")
	if !labExists(multi, "astro-net") {
		t.Fatal("one VM of a multi-VM lab should count as existing")
	}

	single := &config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{}}}}
	mark("astro-one")
	if !labExists(single, "astro-one") {
		t.Fatal("single-VM lab not detected")
	}
}

func TestResetRejectsInvalidConfigBeforeDestroying(t *testing.T) {
	dir := t.TempDir()
	cfg := "metadata: {name: reset-x}\nruntime:\n  kind:\n    nodes: {workers: 99}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd(&rootFlags{})
	root.SetArgs([]string{"reset", "-c", dir, "--yes"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing was reset") || !strings.Contains(err.Error(), "workers") {
		t.Fatalf("error = %v, want the config error before any teardown", err)
	}
}

func TestResetLinkedClusterRefuses(t *testing.T) {
	cfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: "rl"}, Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Labs: []config.KindLab{{Name: "idp"}}}}}
	if err := resetLinkedCluster(cfg, ".", "astro-rl", "nope", true, &rootFlags{}); err == nil || !strings.Contains(err.Error(), "it has: idp") {
		t.Errorf("unknown cluster = %v", err)
	}
	if err := resetLinkedCluster(cfg, ".", "astro-rl-no-such-lab", "idp", true, &rootFlags{}); err == nil || !strings.Contains(err.Error(), "isn't running") {
		t.Errorf("lab not running = %v", err)
	}
}
