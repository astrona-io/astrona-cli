package config

import (
	"fmt"
	"regexp"
	"strings"
)

// PortForward is one entry in runtime.portForwards: a kind-only, host-side
// `kubectl port-forward` astrona keeps running (and restarts) for the life
// of the lab, so a student can reach an in-cluster service from their own
// browser/client without wiring up kubectl themselves.
//
// The listen address is deliberately not configurable — it's always
// 127.0.0.1. A lab config can come from an arbitrary URL or git repo, and
// letting it bind 0.0.0.0 would let a remote config expose the student's
// cluster to their whole network.
type PortForward struct {
	Name        string `yaml:"name"`
	Resource    string `yaml:"resource"`  // "<kind>/<name>", e.g. "svc/frontend"
	Namespace   string `yaml:"namespace"` // defaults to "default"
	HostPort    int    `yaml:"hostPort"`
	TargetPort  int    `yaml:"targetPort"`
	Scheme      string `yaml:"scheme"` // "http" | "https" | "tcp" (default) — only shapes the printed URL
	Description string `yaml:"description"`
	// Cluster forwards from a linked cluster (a runtime.kind.labs name)
	// instead of the lab's own — e.g. an identity provider's login page.
	Cluster string `yaml:"cluster"`
}

// minHostPort keeps forwards out of the privileged port range — binding
// below 1024 needs root on most systems, and astrona never runs as root.
const minHostPort = 1024

// portForwardKinds maps every resource kind spelling kubectl port-forward
// accepts (that this tool allows) to its canonical short form.
var portForwardKinds = map[string]string{
	"svc":         "svc",
	"service":     "svc",
	"pod":         "pod",
	"po":          "pod",
	"deploy":      "deploy",
	"deployment":  "deploy",
	"sts":         "sts",
	"statefulset": "sts",
	"rs":          "rs",
	"replicaset":  "rs",
}

// dns1123Label is the Kubernetes name/namespace rule (also what keeps a
// forward name safe to use as a path component under ~/.astrona).
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// dns1123Subdomain is the looser rule most Kubernetes object names follow
// (dots allowed).
var dns1123Subdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

var validSchemes = map[string]bool{"tcp": true, "http": true, "https": true}

// Normalized returns pf with defaults filled in (namespace "default", scheme
// "tcp") and the resource kind canonicalized ("service/x" -> "svc/x").
// Call only after Validate has passed.
func (pf PortForward) Normalized() PortForward {
	if pf.Namespace == "" {
		pf.Namespace = "default"
	}
	if pf.Scheme == "" {
		pf.Scheme = "tcp"
	}
	pf.Scheme = strings.ToLower(pf.Scheme)
	if kind, name, ok := strings.Cut(pf.Resource, "/"); ok {
		if canon, known := portForwardKinds[strings.ToLower(kind)]; known {
			pf.Resource = canon + "/" + name
		}
	}
	return pf
}

// Validate checks a single entry in isolation. Uniqueness across entries is
// ValidatePortForwards' job.
func (pf PortForward) Validate() error {
	if !dns1123Label.MatchString(pf.Name) {
		return fmt.Errorf("port forward name '%s' must be a lowercase DNS label (a-z, 0-9, '-', max 63 chars)", pf.Name)
	}

	kind, name, ok := strings.Cut(pf.Resource, "/")
	if !ok {
		return fmt.Errorf("port forward '%s': resource '%s' must be '<kind>/<name>', e.g. 'svc/frontend'", pf.Name, pf.Resource)
	}
	if _, known := portForwardKinds[strings.ToLower(kind)]; !known {
		return fmt.Errorf("port forward '%s': unsupported resource kind '%s' (use svc, pod, deploy, sts or rs)", pf.Name, kind)
	}
	if !dns1123Subdomain.MatchString(name) {
		return fmt.Errorf("port forward '%s': resource name '%s' is not a valid Kubernetes name", pf.Name, name)
	}

	if pf.Namespace != "" && !dns1123Label.MatchString(pf.Namespace) {
		return fmt.Errorf("port forward '%s': namespace '%s' is not a valid Kubernetes namespace", pf.Name, pf.Namespace)
	}

	if pf.HostPort < minHostPort || pf.HostPort > 65535 {
		return fmt.Errorf("port forward '%s': hostPort %d must be between %d and 65535", pf.Name, pf.HostPort, minHostPort)
	}
	if pf.TargetPort < 1 || pf.TargetPort > 65535 {
		return fmt.Errorf("port forward '%s': targetPort %d must be between 1 and 65535", pf.Name, pf.TargetPort)
	}

	if pf.Scheme != "" && !validSchemes[strings.ToLower(pf.Scheme)] {
		return fmt.Errorf("port forward '%s': scheme '%s' must be http, https or tcp", pf.Name, pf.Scheme)
	}

	return nil
}

// ValidatePortForwards checks runtime.portForwards: kind-only, every entry
// well-formed, names and host ports unique.
func ValidatePortForwards(rt RuntimeConfig) error {
	if len(rt.PortForwards) == 0 {
		return nil
	}
	if rt.Type != "" && rt.Type != "kind" {
		return fmt.Errorf("runtime.portForwards is only supported for the kind runtime (this lab uses '%s')", rt.Type)
	}

	names := make(map[string]bool, len(rt.PortForwards))
	ports := make(map[int]string, len(rt.PortForwards))
	for _, pf := range rt.PortForwards {
		if err := pf.Validate(); err != nil {
			return err
		}
		if names[pf.Name] {
			return fmt.Errorf("duplicate port forward name '%s' in runtime.portForwards", pf.Name)
		}
		names[pf.Name] = true
		if other, taken := ports[pf.HostPort]; taken {
			return fmt.Errorf("port forwards '%s' and '%s' both use hostPort %d", other, pf.Name, pf.HostPort)
		}
		ports[pf.HostPort] = pf.Name
	}

	return nil
}
