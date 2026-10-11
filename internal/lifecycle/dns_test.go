package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

func TestDNSReadinessTimeout(t *testing.T) {
	for _, c := range []struct {
		name string
		kind *config.KindConfig
		want string
	}{
		{"default", nil, "--timeout=180s"},
		{"custom", &config.KindConfig{DNSTimeout: "7m"}, "--timeout=420s"},
		{"lab installs CNI", &config.KindConfig{Networking: config.KindNetworking{DisableDefaultCNI: true}}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			trace := filepath.Join(dir, "trace")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("DNS_TRACE", trace)
			if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte("#!/bin/sh\necho \"$*\" > \"$DNS_TRACE\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: c.kind}}
			env := &runtime.LabEnvironment{Type: runtime.RuntimeKind, KubeContext: "kind-lab"}
			if err := WaitForClusterDNS(cfg, env, ui.Discard()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(trace)
			if c.want == "" {
				if !os.IsNotExist(err) {
					t.Fatalf("DNS waited before the lab's CNI was installed: %s", data)
				}
				return
			}
			if err != nil || !strings.Contains(string(data), c.want) || !strings.Contains(string(data), "--context kind-lab --namespace kube-system rollout status deploy/coredns") {
				t.Fatalf("DNS gate = %s, err=%v; expected %s", data, err, c.want)
			}
		})
	}
}
