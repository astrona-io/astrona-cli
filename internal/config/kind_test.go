package config

import (
	"strings"
	"testing"
)

func TestValidateKindConfig(t *testing.T) {
	digest := "kindest/node:v1.31.2@sha256:" + strings.Repeat("a", 64)

	cases := []struct {
		name    string
		rt      RuntimeConfig
		wantErr string
	}{
		{"no block", RuntimeConfig{}, ""},
		{"empty block on qemu is ignored", RuntimeConfig{Type: "qemu", Kind: &KindConfig{}}, ""},
		{"version", RuntimeConfig{Kind: &KindConfig{Version: "v1.31.2"}}, ""},
		{"image pinned by digest", RuntimeConfig{Type: "kind", Kind: &KindConfig{Image: digest}}, ""},
		{"image on private registry", RuntimeConfig{Kind: &KindConfig{Image: "localhost:5000/kind/node:v1.31.2"}}, ""},
		{"multi node", RuntimeConfig{Kind: &KindConfig{Nodes: KindNodes{ControlPlanes: 3, Workers: 2}}}, ""},
		{"full networking", RuntimeConfig{Kind: &KindConfig{Networking: KindNetworking{
			DisableDefaultCNI: true, KubeProxyMode: "ipvs", IPFamily: "dual",
			PodSubnet: "10.244.0.0/16", ServiceSubnet: "10.96.0.0/12"}}}, ""},
		{"gates and runtime config", RuntimeConfig{Kind: &KindConfig{
			FeatureGates:  map[string]bool{"InPlacePodVerticalScaling": true},
			RuntimeConfig: map[string]string{"api/alpha": "false", "resource.k8s.io/v1beta1": "true"}}}, ""},

		{"kind block on qemu", RuntimeConfig{Type: "qemu", Kind: &KindConfig{Version: "v1.31.2"}}, "only valid for the kind runtime"},
		{"version and image", RuntimeConfig{Kind: &KindConfig{Version: "v1.31.2", Image: digest}}, "not both"},
		{"bad version", RuntimeConfig{Kind: &KindConfig{Version: "1.31"}}, "must look like"},
		{"image with flag injection", RuntimeConfig{Kind: &KindConfig{Image: "--retain"}}, "not a valid image reference"},
		{"image with space", RuntimeConfig{Kind: &KindConfig{Image: "kindest/node:v1 --config /etc/x"}}, "not a valid image reference"},
		{"too many control planes", RuntimeConfig{Kind: &KindConfig{Nodes: KindNodes{ControlPlanes: 5}}}, "controlPlanes"},
		{"negative workers", RuntimeConfig{Kind: &KindConfig{Nodes: KindNodes{Workers: -1}}}, "workers"},
		{"too many workers", RuntimeConfig{Kind: &KindConfig{Nodes: KindNodes{Workers: 50}}}, "workers"},
		{"bad proxy mode", RuntimeConfig{Kind: &KindConfig{Networking: KindNetworking{KubeProxyMode: "userspace"}}}, "kubeProxyMode"},
		{"bad ip family", RuntimeConfig{Kind: &KindConfig{Networking: KindNetworking{IPFamily: "v4"}}}, "ipFamily"},
		{"bad pod subnet", RuntimeConfig{Kind: &KindConfig{Networking: KindNetworking{PodSubnet: "10.0.0.0"}}}, "podSubnet"},
		{"overlapping subnets", RuntimeConfig{Kind: &KindConfig{Networking: KindNetworking{
			PodSubnet: "10.0.0.0/8", ServiceSubnet: "10.96.0.0/12"}}}, "overlap"},
		{"bad feature gate", RuntimeConfig{Kind: &KindConfig{FeatureGates: map[string]bool{"bad gate": true}}}, "feature gate"},
		{"bad runtime config key", RuntimeConfig{Kind: &KindConfig{RuntimeConfig: map[string]string{"api": "true"}}}, "valid API key"},
		{"bad runtime config value", RuntimeConfig{Kind: &KindConfig{RuntimeConfig: map[string]string{"api/alpha": "yes"}}}, "\"true\" or \"false\""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateKindConfig(c.rt)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

func TestKindConfigDefaultsAndDescribe(t *testing.T) {
	var nilCfg *KindConfig
	if !nilCfg.IsZero() || nilCfg.NodeImage() != "" || nilCfg.ControlPlaneCount() != 1 || nilCfg.WorkerCount() != 0 {
		t.Fatal("nil KindConfig should mean kind's defaults")
	}

	k := &KindConfig{Version: "v1.31.2", Nodes: KindNodes{Workers: 2}, Networking: KindNetworking{DisableDefaultCNI: true}}
	if got := k.NodeImage(); got != "kindest/node:v1.31.2" {
		t.Errorf("NodeImage = %q", got)
	}
	if got, want := k.Describe(), "kindest/node:v1.31.2, 1 control plane + 2 workers, no default CNI"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
	if got, want := (&KindConfig{Nodes: KindNodes{ControlPlanes: 3, Workers: 1}}).Describe(), "3 control planes + 1 worker"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}
