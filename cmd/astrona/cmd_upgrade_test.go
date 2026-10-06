package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsHomebrewInstall(t *testing.T) {
	for _, c := range []struct {
		exe  string
		want bool
	}{
		{"/opt/homebrew/Cellar/astrona/0.3.0/bin/astrona", true},
		{"/usr/local/Cellar/astrona/0.3.0/bin/astrona", true},
		{"/home/linuxbrew/.linuxbrew/Cellar/astrona/0.3.0/bin/astrona", true},
		{"/Users/me/.local/bin/astrona", false},
		{"/usr/local/bin/astrona", false},
		{"/Users/me/.astrona/bin/astrona-0.2.1", false},
		{"/opt/homebrew/Cellar/kind/0.24.0/bin/kind", false},
	} {
		if got := isHomebrewInstall(c.exe); got != c.want {
			t.Errorf("isHomebrewInstall(%q) = %v, want %v", c.exe, got, c.want)
		}
	}
}

// A Homebrew install upgrades through the brew that owns it, never by
// downloading the binary itself.
func TestBrewUpgrade(t *testing.T) {
	prefix := t.TempDir()
	brew := filepath.Join(prefix, "bin", "brew")
	if err := os.MkdirAll(filepath.Dir(brew), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brew, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(prefix, "Cellar", "astrona", "0.3.0", "bin", "astrona")

	var ran [][]string
	old := runBrew
	defer func() { runBrew = old }()
	runBrew = func(c *exec.Cmd) error {
		ran = append(ran, c.Args)
		return nil
	}

	for _, c := range []struct {
		force bool
		verb  string
	}{{false, "upgrade"}, {true, "reinstall"}} {
		ran = nil
		if err := brewUpgrade(exe, c.force); err != nil {
			t.Fatalf("force=%v: %v", c.force, err)
		}
		want := []string{brew, c.verb, "astrona"}
		if len(ran) != 1 || strings.Join(ran[0], " ") != strings.Join(want, " ") {
			t.Errorf("force=%v ran %v, want %v", c.force, ran, want)
		}
	}

	runBrew = func(*exec.Cmd) error { return errors.New("exit status 1") }
	if err := brewUpgrade(exe, false); err == nil || !strings.Contains(err.Error(), "brew upgrade astrona failed") {
		t.Errorf("failing brew = %v", err)
	}
}

// Without the owning prefix's brew, the one on PATH is used; without any,
// the error says what to run.
func TestBrewBinaryFallback(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "Cellar", "astrona", "0.3.0", "bin", "astrona")

	onPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(onPath, "brew"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath)
	if got, err := brewBinary(exe); err != nil || got != filepath.Join(onPath, "brew") {
		t.Errorf("PATH brew = %q, %v", got, err)
	}

	t.Setenv("PATH", t.TempDir())
	if _, err := brewBinary(exe); err == nil || !strings.Contains(err.Error(), "brew upgrade astrona") {
		t.Errorf("no brew = %v", err)
	}
}
