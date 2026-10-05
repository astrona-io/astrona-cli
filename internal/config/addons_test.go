package config

import (
	"strings"
	"testing"
)

func TestValidateKindAddons(t *testing.T) {
	kind := func(k KindConfig) RuntimeConfig { return RuntimeConfig{Kind: &k} }

	cases := []struct {
		name    string
		rt      RuntimeConfig
		wantErr string
	}{
		{"all addons", kind(KindConfig{Addons: KindAddons{CNI: "calico", MetricsServer: true, CertManager: true, GatewayAPI: "envoy"}}), ""},
		{"calico with its own subnet spelled out", kind(KindConfig{Networking: KindNetworking{PodSubnet: CalicoPodSubnet}, Addons: KindAddons{CNI: "calico"}}), ""},
		{"custom gateway ports", kind(KindConfig{Addons: KindAddons{GatewayAPI: "envoy", GatewayPorts: GatewayPorts{HTTP: 9080, HTTPS: 9443}}}), ""},

		{"addons on qemu", RuntimeConfig{Type: "qemu", Kind: &KindConfig{Addons: KindAddons{MetricsServer: true}}}, "only valid for the kind runtime"},
		{"unknown cni", kind(KindConfig{Addons: KindAddons{CNI: "flannel"}}), "cni 'flannel' is not supported"},
		{"calico with other subnet", kind(KindConfig{Networking: KindNetworking{PodSubnet: "10.244.0.0/16"}, Addons: KindAddons{CNI: "calico"}}), "uses pod subnet"},
		{"calico ipv6", kind(KindConfig{Networking: KindNetworking{IPFamily: "ipv6"}, Addons: KindAddons{CNI: "calico"}}), "ipv4"},
		{"unknown gateway", kind(KindConfig{Addons: KindAddons{GatewayAPI: "nginx"}}), "gatewayAPI 'nginx' is not supported"},
		{"ports without gateway", kind(KindConfig{Addons: KindAddons{GatewayPorts: GatewayPorts{HTTP: 9080}}}), "gatewayAPI isn't"},
		{"privileged gateway port", kind(KindConfig{Addons: KindAddons{GatewayAPI: "envoy", GatewayPorts: GatewayPorts{HTTP: 80}}}), "between 1024"},
		{"same gateway ports", kind(KindConfig{Addons: KindAddons{GatewayAPI: "envoy", GatewayPorts: GatewayPorts{HTTP: 9000, HTTPS: 9000}}}), "can't both be"},
		{"gateway port clashes with port forward", RuntimeConfig{
			Kind:         &KindConfig{Addons: KindAddons{GatewayAPI: "envoy"}},
			PortForwards: []PortForward{{Name: "web", Resource: "svc/web", HostPort: 8080, TargetPort: 80}},
		}, "already uses"},
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

func TestEffectiveNetworkingAndPorts(t *testing.T) {
	k := &KindConfig{Networking: KindNetworking{KubeProxyMode: "ipvs"}, Addons: KindAddons{CNI: "calico"}}
	n := k.EffectiveNetworking()
	if !n.DisableDefaultCNI || n.PodSubnet != CalicoPodSubnet || n.KubeProxyMode != "ipvs" {
		t.Fatalf("EffectiveNetworking = %+v", n)
	}
	if n := (&KindConfig{}).EffectiveNetworking(); n != (KindNetworking{}) {
		t.Fatalf("no addons should leave networking alone: %+v", n)
	}

	if p := (KindAddons{GatewayAPI: "envoy"}).EffectiveGatewayPorts(); p != (GatewayPorts{HTTP: 8080, HTTPS: 8443}) {
		t.Fatalf("default ports = %+v", p)
	}
	if !(KindAddons{}).IsZero() || (KindAddons{MetricsServer: true}).IsZero() {
		t.Fatal("KindAddons.IsZero wrong")
	}
	if (&KindConfig{Addons: KindAddons{CertManager: true}}).IsZero() {
		t.Fatal("a kind block with only addons must not count as empty")
	}
	if got := (&KindConfig{Addons: KindAddons{CNI: "calico"}}).Describe(); !strings.Contains(got, "calico CNI") {
		t.Fatalf("Describe = %q", got)
	}
}
