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
	"astrona/internal/ui"
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

// A lab's own strings must not be able to rewrite the trust prompt: no
// escape sequences, and no newlines to fake a line of their own.
func TestTrustPromptSanitized(t *testing.T) {
	evil := "\x1b[1A\x1b[2K\r\n  • fake\x07\x9b"
	cfg := &config.LabConfig{
		Runtime: config.RuntimeConfig{
			Kind:         &config.KindConfig{PreloadImages: []string{"nginx" + evil}, Clusters: []config.KindCluster{{Name: "idp" + evil}}},
			PortForwards: []config.PortForward{{Resource: "svc/web" + evil, Cluster: "idp" + evil, HostPort: 8080}},
		},
		Bootstrap: config.BootstrapConfig{
			Init: []config.ResourceItem{
				{Name: "setup" + evil, Type: "file", Source: "setup.sh" + evil},
				{Name: "remote", Type: "url", Source: "https://x/y.sh" + evil},
			},
		},
		Validation: config.ValidationConfig{Checks: []config.ValidationCheck{{Type: "command", Command: "ls" + evil}}},
	}
	summary := labRiskSummary(cfg)
	for _, l := range summary {
		if hasControl(l) {
			t.Errorf("summary line has control characters: %q", l)
		}
	}
	var out bytes.Buffer
	src := trust.Source{Kind: "url", Location: "https://labs.example/" + evil, Pin: "sha256:abc"}
	confirmTrust(strings.NewReader("n\n"), &out, src, trust.New, "", summary)
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if hasControl(line) {
			t.Errorf("prompt line has control characters: %q", line)
		}
		if strings.HasPrefix(strings.TrimSpace(line), "• fake") {
			t.Errorf("a lab string faked a prompt line: %q", line)
		}
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

// destroy runs a lab's teardown scripts only once the lab is trusted — and
// never fails over it: an unapproved remote lab is destroyed without them.
func TestTeardownTrusted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	script := []config.ResourceItem{{Name: "cleanup", Type: "file", Source: "cleanup.sh"}}
	remote := func(cfg config.LabConfig) teardownInfo {
		cfg.SourcePath, cfg.SourceSHA256 = "https://labs.example/config.yaml", "abc"
		return teardownInfo{teardown: cfg.Teardown, runtime: cfg.Runtime, cfg: &cfg}
	}
	withScript := remote(config.LabConfig{Teardown: config.TeardownConfig{Init: script}})
	linkedScript := remote(config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{
		Clusters: []config.KindCluster{{Name: "idp", Teardown: config.KindClusterTeardown{Init: script}}}}}})
	local := config.LabConfig{SourcePath: filepath.Join(os.TempDir(), "c.yaml"), Teardown: config.TeardownConfig{Init: script}}

	for _, tc := range []struct {
		name  string
		flags rootFlags
		info  teardownInfo
		want  bool
	}{
		{"remote with teardown, no terminal, no --trust", rootFlags{}, withScript, false},
		{"remote linked-cluster teardown, no --trust", rootFlags{}, linkedScript, false},
		{"remote without teardown scripts: nothing to approve", rootFlags{}, remote(config.LabConfig{}), true},
		{"local lab", rootFlags{}, teardownInfo{teardown: local.Teardown, cfg: &local}, true},
		{"no config", rootFlags{}, teardownInfo{teardown: local.Teardown}, true},
		{"remote with --trust", rootFlags{trust: true}, withScript, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := teardownTrusted(&tc.flags, tc.info, "", ui.Discard()); got != tc.want {
				t.Errorf("teardownTrusted = %v, want %v", got, tc.want)
			}
		})
	}
}
