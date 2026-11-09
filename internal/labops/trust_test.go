package labops

import (
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestRiskSummary(t *testing.T) {
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
	got := strings.Join(RiskSummary(cfg), "\n")
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
	if got := RiskSummary(qemu)[0]; !strings.Contains(got, "inside the lab VM(s)") {
		t.Errorf("qemu = %q", got)
	}
	if got := RiskSummary(&config.LabConfig{}); !strings.Contains(got[0], "no scripts") {
		t.Errorf("empty = %v", got)
	}
}
