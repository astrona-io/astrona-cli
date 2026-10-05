package addons

import (
	"errors"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestImagesIn(t *testing.T) {
	manifest := []byte(`
spec:
  containers:
    - name: a
      image: quay.io/calico/node:v3.32.2
    - image: "registry.k8s.io/metrics-server/metrics-server:v0.9.0"
  initContainers:
    - name: b
      image: quay.io/calico/node:v3.32.2
      # image: commented/out:1
`)
	got := strings.Join(ImagesIn(manifest), ",")
	if got != "quay.io/calico/node:v3.32.2,registry.k8s.io/metrics-server/metrics-server:v0.9.0" {
		t.Fatalf("ImagesIn = %s", got)
	}
}

func TestBundleRefusesGatewayAndUnknownManifests(t *testing.T) {
	if _, err := ForBundle(config.KindAddons{GatewayAPI: "envoy"}, nil); !errors.Is(err, ErrNotBundleable) {
		t.Fatalf("gateway = %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	if err := SeedCache([]byte("kind: ConfigMap\n")); err == nil {
		t.Fatal("a manifest that isn't a pinned addon version was accepted into the cache")
	}
}
