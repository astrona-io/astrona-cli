package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/trust"
)

func TestLabSource(t *testing.T) {
	local := &config.LabConfig{SourcePath: "/home/me/lab/config.yaml", SourceSHA256: "abc"}
	if src, err := labSource(&rootFlags{}, local, "/home/me/lab"); src != nil || err != nil {
		t.Fatalf("local config = %+v, %v — must never prompt", src, err)
	}

	remote := &config.LabConfig{SourcePath: "https://labs.example/l1/config.yaml", SourceSHA256: "abc"}
	src, _ := labSource(&rootFlags{}, remote, "")
	if src == nil || src.Kind != "url" || src.Pin != "sha256:abc" {
		t.Fatalf("url source = %+v", src)
	}

	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	src, err := labSource(&rootFlags{gitURL: "https://github.com/org/labs", gitRef: "main", configPath: "labs/l1"}, local, repo)
	if err != nil || src.Kind != "git" || src.Pin != strings.TrimSpace(string(head)) || src.Location != "https://github.com/org/labs@main (labs/l1)" {
		t.Fatalf("git source = %+v, %v", src, err)
	}
}

func TestLabRiskSummary(t *testing.T) {
	cfg := &config.LabConfig{
		Runtime: config.RuntimeConfig{
			Kind: &config.KindConfig{Addons: config.KindAddons{CertManager: true}, PreloadImages: []string{"nginx:1.27"},
				Clusters: []config.KindCluster{{Name: "idp", PreloadImages: []string{"keycloak:26"}, Bootstrap: config.BootstrapConfig{
					Init: []config.ResourceItem{{Name: "realm", Type: "file", Source: "idp/realm.sh"}}}}}},
			PortForwards: []config.PortForward{{Name: "web", Resource: "svc/web", HostPort: 8080}},
		},
		Bootstrap: config.BootstrapConfig{
			Init:      []config.ResourceItem{{Name: "setup", Type: "file", Source: "setup.sh"}, {Name: "remote", Type: "url", Source: "https://x/y.sh"}},
			Manifests: []config.ResourceItem{{Name: "app", Type: "folder", Source: "manifests/"}},
		},
		Validation: config.ValidationConfig{Checks: []config.ValidationCheck{{Type: "command", Command: "kubectl get ns"}}},
	}
	got := strings.Join(labRiskSummary(cfg), "\n")
	for _, want := range []string{
		"runs on this machine (bash): setup (setup.sh)",
		"runs on this machine (command check): kubectl get ns",
		"applies 1 manifest source(s)",
		"installs addons (pinned, checksum-verified): cert-manager",
		"pulls images: nginx:1.27",
		"opens 127.0.0.1:8080 → svc/web",
		"creates linked kind cluster 'idp', pulls images: keycloak:26",
		"runs on this machine (bash): realm (idp/realm.sh)",
		"⚠ fetched when it runs, NOT covered by this approval: https://x/y.sh",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}

	qemu := &config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu"}, Bootstrap: config.BootstrapConfig{Init: []config.ResourceItem{{Name: "s", Type: "file", Source: "s.sh"}}}}
	if got := labRiskSummary(qemu)[0]; !strings.Contains(got, "inside the lab VM(s)") {
		t.Errorf("qemu = %q", got)
	}
	if got := labRiskSummary(&config.LabConfig{}); !strings.Contains(got[0], "no scripts") {
		t.Errorf("empty = %v", got)
	}
}

func TestConfirmTrust(t *testing.T) {
	src := trust.Source{Kind: "git", Location: "https://github.com/org/labs", Pin: "0123456789abcdef"}
	var out bytes.Buffer
	if !confirmTrust(strings.NewReader("y\n"), &out, src, trust.New, "", []string{"runs x"}) {
		t.Error("y not accepted")
	}
	for _, want := range []string{"First time running this lab", "0123456789ab", "• runs x", "[y/N]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if confirmTrust(strings.NewReader("\n"), &out, src, trust.Changed, "ffffffffffffffff", nil) {
		t.Error("empty answer accepted")
	}
	if !strings.Contains(out.String(), "changed since you approved it (ffffffffffff → 0123456789ab)") {
		t.Errorf("changed prompt = %s", out.String())
	}
}

func TestRequireTrust(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	remote := &config.LabConfig{SourcePath: "https://labs.example/config.yaml", SourceSHA256: "abc"}

	// go test's stdin isn't a terminal: refuse without --trust.
	if err := requireTrust(&rootFlags{}, remote, ""); err == nil || !strings.Contains(err.Error(), "--trust") {
		t.Fatalf("untrusted, no terminal = %v", err)
	}
	if err := requireTrust(&rootFlags{trust: true}, remote, ""); err != nil {
		t.Fatalf("--trust = %v", err)
	}
	if err := requireTrust(&rootFlags{}, remote, ""); err != nil {
		t.Fatalf("approved source asked again: %v", err)
	}
	remote.SourceSHA256 = "changed"
	if err := requireTrust(&rootFlags{}, remote, ""); err == nil {
		t.Fatal("changed config ran without approval")
	}
	if err := requireTrust(&rootFlags{}, &config.LabConfig{SourcePath: filepath.Join(os.TempDir(), "c.yaml")}, ""); err != nil {
		t.Fatalf("local config prompted: %v", err)
	}
}
