package authoring

import (
	"testing"

	"astrona/internal/config"
)

func TestBundleRefusal(t *testing.T) {
	ok := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2"}}}
	if r := BundleRefusal(ok, "kindest/node:v1.31.2"); r != "" {
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
		"linked unpinned": {&config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2", Clusters: []config.KindCluster{{Name: "idp"}}}}}, "x"},
		"linked gateway":  {&config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2", Clusters: []config.KindCluster{{Name: "idp", Version: "v1.31.2", Addons: config.KindAddons{GatewayAPI: "envoy"}}}}}}, "x"},
	}
	for name, c := range cases {
		if BundleRefusal(c.cfg, c.node) == "" {
			t.Errorf("%s: not refused", name)
		}
	}
	pinned := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Version: "v1.31.2", Clusters: []config.KindCluster{{Name: "idp", Version: "v1.30.6"}}}}}
	if r := BundleRefusal(pinned, "kindest/node:v1.31.2"); r != "" {
		t.Errorf("lab with a pinned linked cluster refused: %s", r)
	}
}
