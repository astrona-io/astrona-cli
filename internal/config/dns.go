package config

import (
	"fmt"
	"net/netip"
)

// DefaultDNS are the upstream servers a kind lab's network uses when the
// lab sets no runtime.kind.dns: Google Public DNS, so a slow or broken host
// resolver (a VPN, a captive network) does not stall image pulls.
var DefaultDNS = []string{"8.8.8.8", "8.8.4.4"}

// maxDNS bounds runtime.kind.dns: resolvers only ever try the first few.
const maxDNS = 3

// DNSServers are the upstream servers for the lab's container network:
// runtime.kind.dns, else DefaultDNS.
func (k *KindConfig) DNSServers() []string {
	if k != nil && len(k.DNS) > 0 {
		return k.DNS
	}
	return DefaultDNS
}

func validateDNS(servers []string) error {
	if len(servers) > maxDNS {
		return fmt.Errorf("runtime.kind.dns: %d servers listed; at most %d are used", len(servers), maxDNS)
	}
	for _, server := range servers {
		if _, err := netip.ParseAddr(server); err != nil {
			return fmt.Errorf("runtime.kind.dns: '%s' is not an IP address (e.g. 8.8.8.8)", server)
		}
	}
	return nil
}
