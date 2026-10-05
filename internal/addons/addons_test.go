package addons

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/config"

	"gopkg.in/yaml.v3"
)

func TestSelectedOrder(t *testing.T) {
	all := selected(config.KindAddons{CNI: "calico", MetricsServer: true, CertManager: true, GatewayAPI: "envoy"})
	var names []string
	for _, a := range all {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, ","); got != "Calico CNI,cert-manager,metrics-server,Envoy Gateway (Gateway API)" {
		t.Fatalf("install order = %s", got)
	}
	if len(selected(config.KindAddons{})) != 0 {
		t.Fatal("no addons enabled should select nothing")
	}
}

func TestCatalogIsPinned(t *testing.T) {
	for _, a := range []addon{calico, certManager, metricsServer, envoyGateway} {
		if !strings.HasPrefix(a.Manifest.URL, "https://") {
			t.Errorf("%s: manifest URL must be https: %s", a.Name, a.Manifest.URL)
		}
		if !strings.Contains(a.Manifest.URL, a.Version) {
			t.Errorf("%s: URL %s doesn't contain pinned version %s", a.Name, a.Manifest.URL, a.Version)
		}
		if b, err := hex.DecodeString(a.Manifest.SHA256); err != nil || len(b) != sha256.Size {
			t.Errorf("%s: SHA256 %q is not a sha256 hex digest", a.Name, a.Manifest.SHA256)
		}
		for _, w := range a.Ready {
			if err := w.Validate("addon"); err != nil {
				t.Errorf("%s: invalid readiness gate: %v", a.Name, err)
			}
		}
	}
}

func TestGatewayResources(t *testing.T) {
	dec := yaml.NewDecoder(strings.NewReader(gatewayResources))
	var svc map[string]any
	kinds := map[string]bool{}
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("gatewayResources is not valid YAML: %v", err)
		}
		kinds[doc["kind"].(string)] = true
		if doc["kind"] == "Service" {
			svc = doc
		}
	}
	for _, k := range []string{"EnvoyProxy", "GatewayClass", "Service"} {
		if !kinds[k] {
			t.Errorf("missing %s", k)
		}
	}

	spec := svc["spec"].(map[string]any)
	sel := spec["selector"].(map[string]any)
	if sel["gateway.envoyproxy.io/owning-gatewayclass"] != GatewayClassName {
		t.Errorf("service selector = %v", sel)
	}
	var nodePorts []int
	for _, p := range spec["ports"].([]any) {
		nodePorts = append(nodePorts, p.(map[string]any)["nodePort"].(int))
	}
	if len(nodePorts) != 2 || nodePorts[0] != cluster.GatewayNodePortHTTP || nodePorts[1] != cluster.GatewayNodePortHTTPS {
		t.Errorf("nodePorts = %v, must match the kind port mappings", nodePorts)
	}
}

func TestFetchUsesVerifiedCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	content := []byte("kind: ConfigMap\n")
	sum := sha256.Sum256(content)
	m := manifest{URL: "https://127.0.0.1:1/unreachable.yaml", SHA256: hex.EncodeToString(sum[:])}

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(dir, m.SHA256+".yaml")
	if err := os.WriteFile(cached, content, 0600); err != nil {
		t.Fatal(err)
	}

	path, err := fetch(m, io.Discard)
	if err != nil || path != cached {
		t.Fatalf("fetch = %q, %v; want the cached file without touching the network", path, err)
	}
}

func TestFetchDiscardsTamperedCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := manifest{URL: "https://127.0.0.1:1/unreachable.yaml", SHA256: strings.Repeat("ab", 32)}

	dir, _ := cacheDir()
	cached := filepath.Join(dir, m.SHA256+".yaml")
	if err := os.WriteFile(cached, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := fetch(m, io.Discard); err == nil {
		t.Fatal("fetch succeeded with a tampered cache and no network")
	}
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Fatal("tampered cache file was not removed")
	}
}

func TestVerify(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	if err := verify([]byte("x"), hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := verify([]byte("y"), hex.EncodeToString(sum[:])); !errors.Is(err, errChecksum) {
		t.Fatalf("verify mismatch = %v", err)
	}
}
