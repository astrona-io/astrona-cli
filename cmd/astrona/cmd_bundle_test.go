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
		"qemu":        {&config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu"}}, "x"},
		"url script":  {&config.LabConfig{Bootstrap: config.BootstrapConfig{Init: []config.ResourceItem{{Name: "s", Type: "url", Source: "https://x/s.sh"}}}}, "x"},
		"gateway":     {&config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Addons: config.KindAddons{GatewayAPI: "envoy"}}}}, "x"},
		"no node pin": {&config.LabConfig{}, ""},
	}
	for name, c := range cases {
		if bundleRefusal(c.cfg, c.node) == "" {
			t.Errorf("%s: not refused", name)
		}
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
