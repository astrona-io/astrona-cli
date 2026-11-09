package main

import (
	"testing"

	"astrona/internal/preflight"
)

// With Homebrew installed, a missing tool's hint is the brew command, and
// everything missing installs with one line; without it, the web pages.
func TestBrewInstallHints(t *testing.T) {
	old := brewAvailable
	defer func() { brewAvailable = old }()

	kind := preflight.DepCheck{Name: "kind", InstallHint: "https://kind.example", BrewFormula: "kind"}
	engine := preflight.DepCheck{Name: "docker or podman", InstallHint: "https://docker.example", BrewFormula: "podman"}
	qemuImg := preflight.DepCheck{Name: "qemu-img", InstallHint: "apt install qemu-utils", BrewFormula: "qemu"}
	qemuArm := preflight.DepCheck{Name: "qemu-system-aarch64", InstallHint: "apt install qemu-system-arm", BrewFormula: "qemu"}
	ssh := preflight.DepCheck{Name: "ssh", InstallHint: "usually preinstalled"}

	brewAvailable = func() bool { return true }
	for _, c := range []struct {
		dep  preflight.DepCheck
		want string
	}{
		{kind, "brew install kind"},
		{engine, "brew install podman && podman machine init && podman machine start — or Docker Desktop"},
		{ssh, "usually preinstalled"},
	} {
		if got := depHint(c.dep); got != c.want {
			t.Errorf("%s hint = %q, want %q", c.dep.Name, got, c.want)
		}
	}
	if got := brewInstallAll([]preflight.DepCheck{kind, qemuImg, ssh, qemuArm, engine}); got != "brew install kind qemu podman" {
		t.Errorf("brewInstallAll = %q", got)
	}
	if got := brewInstallAll([]preflight.DepCheck{ssh}); got != "" {
		t.Errorf("nothing brew can install = %q, want empty", got)
	}

	brewAvailable = func() bool { return false }
	if got := depHint(kind); got != "https://kind.example" {
		t.Errorf("no brew: hint = %q", got)
	}
	if got := brewInstallAll([]preflight.DepCheck{kind}); got != "" {
		t.Errorf("no brew: brewInstallAll = %q", got)
	}
}

// Every dependency Homebrew packages names its formula.
func TestDepChecksHaveBrewFormulas(t *testing.T) {
	want := map[string]string{
		"kind": "kind", "docker or podman": "podman", "kubectl": "kubernetes-cli", "git": "git",
		"qemu-system-x86_64": "qemu", "qemu-system-aarch64": "qemu", "qemu-img": "qemu", "oras": "oras",
		"mkisofs / genisoimage / xorriso / hdiutil": "cdrtools",
	}
	for _, c := range preflight.AstronaDepChecks() {
		if f, ok := want[c.Name]; ok && c.BrewFormula != f {
			t.Errorf("%s: brewFormula = %q, want %q", c.Name, c.BrewFormula, f)
		}
	}
}
