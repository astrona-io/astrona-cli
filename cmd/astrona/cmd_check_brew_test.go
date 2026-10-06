package main

import "testing"

// With Homebrew installed, a missing tool's hint is the brew command, and
// everything missing installs with one line; without it, the web pages.
func TestBrewInstallHints(t *testing.T) {
	old := brewAvailable
	defer func() { brewAvailable = old }()

	kind := depCheck{name: "kind", installHint: "https://kind.example", brewFormula: "kind"}
	engine := depCheck{name: "docker or podman", installHint: "https://docker.example", brewFormula: "podman"}
	qemuImg := depCheck{name: "qemu-img", installHint: "apt install qemu-utils", brewFormula: "qemu"}
	qemuArm := depCheck{name: "qemu-system-aarch64", installHint: "apt install qemu-system-arm", brewFormula: "qemu"}
	ssh := depCheck{name: "ssh", installHint: "usually preinstalled"}

	brewAvailable = func() bool { return true }
	for _, c := range []struct {
		dep  depCheck
		want string
	}{
		{kind, "brew install kind"},
		{engine, "brew install podman && podman machine init && podman machine start — or Docker Desktop"},
		{ssh, "usually preinstalled"},
	} {
		if got := c.dep.hint(); got != c.want {
			t.Errorf("%s hint = %q, want %q", c.dep.name, got, c.want)
		}
	}
	if got := brewInstallAll([]depCheck{kind, qemuImg, ssh, qemuArm, engine}); got != "brew install kind qemu podman" {
		t.Errorf("brewInstallAll = %q", got)
	}
	if got := brewInstallAll([]depCheck{ssh}); got != "" {
		t.Errorf("nothing brew can install = %q, want empty", got)
	}

	brewAvailable = func() bool { return false }
	if got := kind.hint(); got != "https://kind.example" {
		t.Errorf("no brew: hint = %q", got)
	}
	if got := brewInstallAll([]depCheck{kind}); got != "" {
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
	for _, c := range astronaDepChecks() {
		if f, ok := want[c.name]; ok && c.brewFormula != f {
			t.Errorf("%s: brewFormula = %q, want %q", c.name, c.brewFormula, f)
		}
	}
}
