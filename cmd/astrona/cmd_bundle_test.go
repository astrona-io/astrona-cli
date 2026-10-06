package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestBundleRefusal(t *testing.T) {
	ok := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2"}}}
	if r := bundleRefusal(ok, "kindest/node:v1.31.2"); r != "" {
		t.Fatalf("valid lab refused: %s", r)
	}
	cases := map[string]struct {
		cfg  *config.LabConfig
		node string
	}{
		"qemu":            {&config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu"}}, "x"},
		"url script":      {&config.LabConfig{Bootstrap: config.BootstrapConfig{Init: []config.ResourceItem{{Name: "s", Type: "url", Source: "https://x/s.sh"}}}}, "x"},
		"gateway":         {&config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Addons: config.KindAddons{GatewayAPI: "envoy"}}}}, "x"},
		"no node pin":     {&config.LabConfig{}, ""},
		"linked unpinned": {&config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2", Labs: []config.KindLab{{Name: "idp"}}}}}, "x"},
		"linked gateway":  {&config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2", Labs: []config.KindLab{{Name: "idp", Version: "v1.31.2", Addons: config.KindAddons{GatewayAPI: "envoy"}}}}}}, "x"},
	}
	for name, c := range cases {
		if bundleRefusal(c.cfg, c.node) == "" {
			t.Errorf("%s: not refused", name)
		}
	}
	pinned := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2", Labs: []config.KindLab{{Name: "idp", Version: "v1.30.6"}}}}}
	if r := bundleRefusal(pinned, "kindest/node:v1.31.2"); r != "" {
		t.Errorf("lab with a pinned linked cluster refused: %s", r)
	}
}

func TestLabPreloadImagesWithSidecar(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{PreloadImages: []string{"nginx:1.27"}}}}
	if got := labPreloadImages(cfg, dir); strings.Join(got, ",") != "nginx:1.27" {
		t.Fatalf("no sidecar = %v", got)
	}
	os.WriteFile(filepath.Join(dir, bundleImagesSidecar), []byte(`["quay.io/calico/node:v3.32.2","nginx:1.27"]`), 0600)
	if got := labPreloadImages(cfg, dir); strings.Join(got, ",") != "nginx:1.27,quay.io/calico/node:v3.32.2" {
		t.Fatalf("with sidecar = %v (deduped, lab's own first)", got)
	}
	if got := labPreloadImages(&config.LabConfig{}, dir); len(got) != 2 {
		t.Fatalf("no kind block + sidecar = %v", got)
	}
}

func TestLoadLabForCommandAppliesBundleImages(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("metadata: {name: b}\n"), 0600)

	cfg, _, cleanup, err := LoadLabForCommand(&rootFlags{configPath: dir, fileName: "config.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if cfg.Runtime.Kind != nil {
		t.Fatal("a normal lab must stay untouched (no kind block added)")
	}

	os.WriteFile(filepath.Join(dir, bundleImagesSidecar), []byte(`["registry.k8s.io/metrics-server/metrics-server:v0.9.0"]`), 0600)
	cfg, _, cleanup, err = LoadLabForCommand(&rootFlags{configPath: dir, fileName: "config.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if cfg.Runtime.Kind == nil || strings.Join(cfg.Runtime.Kind.PreloadImages, ",") != "registry.k8s.io/metrics-server/metrics-server:v0.9.0" {
		t.Fatalf("bundle images not applied: %+v", cfg.Runtime.Kind)
	}
}

func TestApplyBundleImagesToLinkedClusters(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, bundleImagesSidecar), []byte(`["quay.io/jetstack/cert-manager-controller:v1.21.2"]`), 0600)
	cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Labs: []config.KindLab{
		{Name: "idp", PreloadImages: []string{"nginx:1.27"}, Addons: config.KindAddons{CertManager: true}},
		{Name: "db", PreloadImages: []string{"postgres:17"}},
	}}}}
	applyBundleImages(cfg, dir)
	labs := cfg.Runtime.Kind.Labs
	if strings.Join(labs[0].PreloadImages, ",") != "nginx:1.27,quay.io/jetstack/cert-manager-controller:v1.21.2" {
		t.Errorf("linked cluster with addons = %v", labs[0].PreloadImages)
	}
	if strings.Join(labs[1].PreloadImages, ",") != "postgres:17" {
		t.Errorf("linked cluster without addons got addon images: %v", labs[1].PreloadImages)
	}
}
