package cluster

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// LinkDomain is the DNS suffix of linked clusters' stable names:
// <name>.astrona.internal resolves, inside every cluster of the lab, to the
// linked cluster's node — the same name under
// `astrona run` and `astrona test`, unlike its container name. (.internal
// is reserved for private use; *.localhost wouldn't work — curl and others
// resolve it to 127.0.0.1 without asking DNS.)
const LinkDomain = "astrona.internal"

const (
	rewriteBegin = "    # astrona-links begin (managed by astrona)"
	rewriteEnd   = "    # astrona-links end"
)

var (
	serverBlock    = regexp.MustCompile(`(?m)^\.:53 \{[ \t]*$`)
	managedRewrite = regexp.MustCompile(`(?s)\n` + regexp.QuoteMeta(rewriteBegin) + `.*?` + regexp.QuoteMeta(rewriteEnd))
)

// WithLinkHosts returns corefile with a CoreDNS hosts block mapping each
// stable name (<name>.astrona.internal) to its node's IPv4 — looked up by
// astrona, not left to the container network's DNS, which after a restart
// may answer a bare container name with an IPv6 address only. Re-applied
// whenever IPs may have changed (run, start, reset --cluster); a previous
// astrona block is replaced. Errors if the Corefile has no `.:53 {` server
// block (a lab-customized CoreDNS).
func WithLinkHosts(corefile string, hosts map[string]string) (string, error) {
	corefile = managedRewrite.ReplaceAllString(corefile, "")
	loc := serverBlock.FindStringIndex(corefile)
	if loc == nil {
		return "", fmt.Errorf("CoreDNS config has no '.:53 {' server block to add linked cluster names to")
	}
	names := make([]string, 0, len(hosts))
	for n := range hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("\n" + rewriteBegin + "\n    hosts {\n")
	for _, n := range names {
		fmt.Fprintf(&b, "        %s %s\n", hosts[n], n)
	}
	b.WriteString("        fallthrough\n    }\n" + rewriteEnd)
	return corefile[:loc[1]] + b.String() + corefile[loc[1]:], nil
}

// NodeIPv4 is the IPv4 of kind cluster clusterName's control-plane node on
// the shared "kind" container network — what its NodePorts answer on.
func NodeIPv4(clusterName string) (string, error) {
	engine, err := DetectContainerEngine()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(engine.Path, "inspect", "--format",
		`{{with index .NetworkSettings.Networks "kind"}}{{.IPAddress}}{{end}}`,
		clusterName+"-control-plane").Output()
	if err != nil {
		return "", fmt.Errorf("%s inspect %s-control-plane: %w", engine.Name, clusterName, err)
	}
	ip := net.ParseIP(strings.TrimSpace(string(out)))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("%s-control-plane has no IPv4 address on the kind network", clusterName)
	}
	return ip.String(), nil
}
