package config

import "fmt"

// KindAddons is runtime.kind.addons: well-known cluster components astrona
// installs right after the kind cluster is created, before bootstrap. Each
// is pinned to one upstream release whose manifest checksum is compiled
// into astrona (see internal/addons) — a lab picks *which* addons, never
// *what* gets applied.
type KindAddons struct {
	// CNI replaces kindnet: "calico" (the only one so far). Implies
	// networking.disableDefaultCNI and a 192.168.0.0/16 pod subnet
	// (Calico's default pool).
	CNI           string `yaml:"cni"`
	MetricsServer bool   `yaml:"metricsServer"`
	CertManager   bool   `yaml:"certManager"`
	// GatewayAPI installs Gateway API + an implementation: "envoy" (Envoy
	// Gateway). Gateways of class "eg" listening on 80/443 become reachable
	// from the host on 127.0.0.1:GatewayPorts.
	GatewayAPI   string       `yaml:"gatewayAPI"`
	GatewayPorts GatewayPorts `yaml:"gatewayPorts"`

	// SkipHostPorts drops the gateway's host port mappings — set by
	// `astrona test`, whose throwaway cluster must not fight a real `run`
	// of the same lab for the same host ports. Not settable from YAML.
	SkipHostPorts bool `yaml:"-"`
}

// GatewayPorts are the host ports (on 127.0.0.1) the gateway's HTTP/HTTPS
// listeners are published on.
type GatewayPorts struct {
	HTTP  int `yaml:"http"`
	HTTPS int `yaml:"https"`
}

const (
	CNICalico          = "calico"
	CalicoPodSubnet    = "192.168.0.0/16"
	GatewayEnvoy       = "envoy"
	defaultGatewayHTTP = 8080
	defaultGatewayTLS  = 8443
)

// IsZero reports whether no addon is enabled.
func (a KindAddons) IsZero() bool {
	return a.CNI == "" && !a.MetricsServer && !a.CertManager && a.GatewayAPI == "" && a.GatewayPorts == GatewayPorts{}
}

// EffectiveGatewayPorts applies the 8080/8443 defaults.
func (a KindAddons) EffectiveGatewayPorts() GatewayPorts {
	p := a.GatewayPorts
	if p.HTTP == 0 {
		p.HTTP = defaultGatewayHTTP
	}
	if p.HTTPS == 0 {
		p.HTTPS = defaultGatewayTLS
	}
	return p
}

// EffectiveNetworking is runtime.kind.networking with what the addons
// imply applied on top (a CNI addon disables kindnet and fixes the pod
// subnet). Call only after ValidateKindConfig.
func (k *KindConfig) EffectiveNetworking() KindNetworking {
	if k == nil {
		return KindNetworking{}
	}
	n := k.Networking
	if k.Addons.CNI == CNICalico {
		n.DisableDefaultCNI = true
		if n.PodSubnet == "" {
			n.PodSubnet = CalicoPodSubnet
		}
	}
	return n
}

func validateKindAddons(rt RuntimeConfig) error {
	a := rt.Kind.Addons

	switch a.CNI {
	case "":
	case CNICalico:
		if p := rt.Kind.Networking.PodSubnet; p != "" && p != CalicoPodSubnet {
			return fmt.Errorf("runtime.kind.addons.cni calico uses pod subnet %s — remove networking.podSubnet or set it to that", CalicoPodSubnet)
		}
		if rt.Kind.Networking.IPFamily == "ipv6" {
			return fmt.Errorf("runtime.kind.addons.cni calico is only supported with ipFamily ipv4 (or unset)")
		}
	default:
		return fmt.Errorf("runtime.kind.addons.cni '%s' is not supported (use 'calico')", a.CNI)
	}

	switch a.GatewayAPI {
	case "":
		if a.GatewayPorts != (GatewayPorts{}) {
			return fmt.Errorf("runtime.kind.addons.gatewayPorts is set but gatewayAPI isn't — set gatewayAPI: envoy")
		}
	case GatewayEnvoy:
		p := a.EffectiveGatewayPorts()
		for _, port := range []int{p.HTTP, p.HTTPS} {
			if port < 1024 || port > 65535 {
				return fmt.Errorf("runtime.kind.addons.gatewayPorts: %d must be between 1024 and 65535", port)
			}
		}
		if p.HTTP == p.HTTPS {
			return fmt.Errorf("runtime.kind.addons.gatewayPorts: http and https can't both be %d", p.HTTP)
		}
		for _, pf := range rt.PortForwards {
			if pf.HostPort == p.HTTP || pf.HostPort == p.HTTPS {
				return fmt.Errorf("port forward '%s' uses hostPort %d, which runtime.kind.addons.gatewayPorts already uses", pf.Name, pf.HostPort)
			}
		}
	default:
		return fmt.Errorf("runtime.kind.addons.gatewayAPI '%s' is not supported (use 'envoy')", a.GatewayAPI)
	}

	return nil
}
