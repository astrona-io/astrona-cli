package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Link is one entry in a lab's top-level links: another kind lab this one
// works with — e.g. an identity provider the app under test logs in
// through. `astrona run` starts linked labs first (each stays a normal,
// independent lab with its own cluster) and tells this lab where they
// are: scripts and command checks get ASTRONA_LINK_<NAME>_HOST /
// _CONTEXT / _KUBECONFIG, and the cluster gets a ConfigMap astrona-links.
//
// All kind clusters share one container network, so a pod here reaches a
// linked lab's NodePort service at <HOST>:<nodePort>; ClusterIP services
// and pod IPs aren't routable between clusters.
type Link struct {
	// Name is how this lab refers to the link (env vars, `link:` on
	// checks): a short DNS label.
	Name string `yaml:"name"`
	// Lab is the linked lab: a path to its config dir/file (relative to
	// this config, e.g. "../idp-lab"), or the name of a lab that is
	// already running (e.g. "idp-lab").
	Lab string `yaml:"lab"`
}

const maxLinks = 5

var linkNamePattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,18}[a-z0-9])?$`)

// IsPath reports whether Lab points at a config on disk rather than at a
// running lab's name.
func (l Link) IsPath() bool {
	return strings.ContainsAny(l.Lab, `/\`) || l.Lab == "." || l.Lab == ".." ||
		strings.HasSuffix(l.Lab, ".yaml") || strings.HasSuffix(l.Lab, ".yml")
}

// EnvPrefix is the environment variable prefix for this link, e.g.
// "ASTRONA_LINK_IDP" for name "idp".
func (l Link) EnvPrefix() string {
	return "ASTRONA_LINK_" + strings.ToUpper(strings.ReplaceAll(l.Name, "-", "_"))
}

// ValidateLinks checks the links block. Whether a linked lab exists and is
// itself a valid kind lab is checked when it's resolved.
func ValidateLinks(cfg *LabConfig) error {
	if len(cfg.Links) == 0 {
		return nil
	}
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return fmt.Errorf("links are only supported for kind labs (this lab uses '%s')", cfg.Runtime.Type)
	}
	if len(cfg.Links) > maxLinks {
		return fmt.Errorf("links has %d entries — at most %d", len(cfg.Links), maxLinks)
	}
	seen := map[string]bool{}
	for _, l := range cfg.Links {
		if !linkNamePattern.MatchString(l.Name) {
			return fmt.Errorf("link name '%s' must be a short lowercase name (a-z, 0-9, '-', max 20, starting with a letter)", l.Name)
		}
		if seen[l.Name] {
			return fmt.Errorf("duplicate link name '%s'", l.Name)
		}
		seen[l.Name] = true
		if strings.TrimSpace(l.Lab) == "" {
			return fmt.Errorf("link '%s' needs lab: a path to the linked lab's config, or a running lab's name", l.Name)
		}
		if !l.IsPath() && !linkLabNamePattern.MatchString(l.Lab) {
			return fmt.Errorf("link '%s': '%s' is neither a path (use ./ or ../) nor a valid lab name", l.Name, l.Lab)
		}
	}
	return nil
}

var linkLabNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
