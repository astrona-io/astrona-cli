package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/version"
)

func fakeBinary(t *testing.T, dir, name string, executable bool) {
	t.Helper()
	mode := os.FileMode(0644)
	if executable {
		mode = 0755
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledVersionsAndNewestAllowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".astrona", "bin")
	os.MkdirAll(bin, 0755)
	other := t.TempDir()
	t.Setenv("PATH", other)

	fakeBinary(t, bin, "astrona-0.2.1", true)
	fakeBinary(t, bin, "astrona-0.1.9", true)
	fakeBinary(t, other, "astrona-0.2.1", true) // same version on PATH: ~/.astrona/bin wins
	fakeBinary(t, other, "astrona-0.3.0", true)
	fakeBinary(t, other, "astrona-0.2.5", false) // not executable
	fakeBinary(t, other, "astrona-latest", true) // not a version

	var got []string
	for _, iv := range installedVersions() {
		got = append(got, iv.v.String()+"@"+filepath.Base(filepath.Dir(iv.path)))
	}
	if strings.Join(got, " ") != "0.3.0@"+filepath.Base(other)+" 0.2.1@bin 0.1.9@bin" {
		t.Errorf("installed = %v", got)
	}

	c, _ := version.ParseConstraint("<=0.2.1")
	if iv, ok := newestAllowed(c, installedVersions()); !ok || iv.v.String() != "0.2.1" {
		t.Errorf("newest allowed for <=0.2.1 = %+v, %v", iv, ok)
	}
	c, _ = version.ParseConstraint(">=1.0.0")
	if _, ok := newestAllowed(c, installedVersions()); ok {
		t.Error("nothing should match >=1.0.0")
	}
}

func TestHandoverArgs(t *testing.T) {
	f := &rootFlags{configPath: "/labs/web", fileName: "config.yaml", labArg: "./labs/web"}
	got := strings.Join(handoverArgs([]string{"run", "./labs/web", "--verbose"}, f), " ")
	if got != "run --verbose -c /labs/web -f config.yaml" {
		t.Errorf("lab argument = %s", got)
	}
	g := &rootFlags{configPath: "labs/net", fileName: "config.yaml", gitURL: "https://github.com/org/labs", gitRef: "v2"}
	got = strings.Join(handoverArgs([]string{"submit"}, g), " ")
	if got != "submit -c labs/net -f config.yaml --git https://github.com/org/labs --git-ref v2" {
		t.Errorf("git lab = %s", got)
	}
}

func TestEnsureLabVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	releaseTags = func() ([]string, error) { return []string{"v0.3.0", "v0.2.1", "v0.2.0"}, nil }
	defer func() { releaseTags = githubReleaseTags }()
	old := Version
	defer func() { Version = old }()

	Version = "v0.2.2"
	if err := ensureLabVersion("", &rootFlags{}); err != nil {
		t.Errorf("no constraint: %v", err)
	}
	if err := ensureLabVersion(">=0.2.0", &rootFlags{}); err != nil {
		t.Errorf("allowed: %v", err)
	}
	err := ensureLabVersion("<=0.2.1", &rootFlags{})
	if err == nil || !strings.Contains(err.Error(), "astrona versions install 0.2.1") {
		t.Errorf("not allowed, none installed = %v — must say what to install", err)
	}
	if err := ensureLabVersion("~0.2", &rootFlags{}); err == nil {
		t.Error("bad constraint accepted")
	}

	t.Setenv(dispatchedEnv, "0.3.0")
	if err := ensureLabVersion("<=0.2.1", &rootFlags{}); err == nil || !strings.Contains(err.Error(), "handed it to") {
		t.Errorf("second hand-over must stop: %v", err)
	}

	Version = "developer"
	if err := ensureLabVersion("<=0.2.1", &rootFlags{}); err != nil {
		t.Errorf("developer build should not be blocked: %v", err)
	}
}

func TestNewerRelease(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.2", "v0.2.1", true},
		{"v0.2.1", "v0.2.2", false},
		{"v0.2.1", "v0.2.1", false},
		{"v0.3.0", "v0.3.0-rc1", true},
		{"v0.2.1", "developer", false},
		{"", "v0.2.1", false},
	} {
		if got := newerRelease(c.latest, c.current); got != c.want {
			t.Errorf("newerRelease(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}
