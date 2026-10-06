package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/version"

	"github.com/mattn/go-isatty"
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
	stdinIsTerminal = func() bool { return false }
	defer func() { stdinIsTerminal = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) } }()
	err := ensureLabVersion("<=0.2.1", &rootFlags{})
	if err == nil || !strings.Contains(err.Error(), "astrona versions install 0.2.1") || !strings.Contains(err.Error(), "--install-version") {
		t.Errorf("not allowed, none installed, no terminal = %v — must say what to install", err)
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

// When no allowed version is installed: --install-version installs it, a
// yes at the terminal installs it, a no doesn't — and then the command is
// handed over to it, without our own --install-version.
func TestOfferInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv(dispatchedEnv, "")
	old, origInstall, origExec, origTerm := Version, installVersion, execHandover, stdinIsTerminal
	Version = "v0.2.2"
	releaseTags = func() ([]string, error) { return []string{"v0.3.0", "v0.2.1", "v0.2.0"}, nil }
	var installed []string
	installVersion = func(v version.V) (string, error) {
		installed = append(installed, v.String())
		return "/fake/astrona-" + v.String(), nil
	}
	errHandedOver := errors.New("handed over")
	var handedArgs []string
	execHandover = func(path string, argv, env []string) error {
		handedArgs = argv
		return errHandedOver
	}
	origArgs := os.Args
	defer func() {
		Version, releaseTags, installVersion, execHandover, stdinIsTerminal = old, githubReleaseTags, origInstall, origExec, origTerm
		promptIn, os.Args = os.Stdin, origArgs
	}()

	// --install-version: no question asked, even without a terminal.
	stdinIsTerminal = func() bool { return false }
	os.Args = []string{"astrona", "run", "--install-version"}
	f := &rootFlags{configPath: "/labs/old", fileName: "config.yaml", installVersion: true}
	if err := ensureLabVersion("<=0.2.1", f); !errors.Is(err, errHandedOver) {
		t.Fatalf("--install-version = %v", err)
	}
	if strings.Join(installed, ",") != "0.2.1" {
		t.Errorf("installed = %v — want the newest allowed release", installed)
	}
	if strings.Contains(strings.Join(handedArgs, " "), "--install-version") || handedArgs[0] != "/fake/astrona-0.2.1" {
		t.Errorf("handed over as %v — the older version doesn't know --install-version", handedArgs)
	}

	// At a terminal: "y" installs, "n" doesn't.
	stdinIsTerminal = func() bool { return true }
	installed = nil
	os.Args = []string{"astrona", "run"}
	promptIn = strings.NewReader("y\n")
	if err := ensureLabVersion("<=0.2.1", &rootFlags{configPath: "/labs/old", fileName: "config.yaml"}); !errors.Is(err, errHandedOver) || len(installed) != 1 {
		t.Errorf("yes = %v, installed %v", err, installed)
	}
	installed = nil
	promptIn = strings.NewReader("n\n")
	err := ensureLabVersion("<=0.2.1", &rootFlags{configPath: "/labs/old", fileName: "config.yaml"})
	if err == nil || errors.Is(err, errHandedOver) || len(installed) != 0 || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("no = %v, installed %v", err, installed)
	}

	// No published release fits: say so, install nothing.
	installed = nil
	promptIn = strings.NewReader("y\n")
	if err := ensureLabVersion(">=9.0.0", &rootFlags{}); err == nil || len(installed) != 0 || !strings.Contains(err.Error(), "no published release fits") {
		t.Errorf("nothing fits = %v, installed %v", err, installed)
	}
}
