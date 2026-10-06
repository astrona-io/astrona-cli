package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"astrona/internal/config"
)

// LinkState is one of a lab's linked clusters (runtime.kind.clusters): its
// name in the config and its kind cluster. Saved at `astrona run` time in
// ~/.astrona/kind/<lab>/links.json, so later commands (submit, shell,
// status, stop/start, destroy) know a lab's linked clusters without
// needing its config.
type LinkState struct {
	Name    string `json:"name"`
	Cluster string `json:"cluster"`
	// WAN is the cluster's configured network conditions — kept so
	// `astrona start` can re-apply them (a restart drops them).
	WAN config.WANConditions `json:"wan,omitzero"`
}

// Host is the linked cluster's control-plane container name — resolvable
// from any kind node (and so any pod) on the shared container network.
func (l LinkState) Host() string { return l.Cluster + "-control-plane" }

// Hostname is the linked cluster's stable name, <name>.astrona.internal —
// see LinkDomain.
func (l LinkState) Hostname() string { return l.Name + "." + LinkDomain }

// Context is the linked cluster's kube context name.
func (l LinkState) Context() string { return "kind-" + l.Cluster }

// EnvPrefix is the cluster's environment variable prefix, e.g.
// ASTRONA_CLUSTER_IDP — mirrors config.KindCluster.EnvPrefix.
func (l LinkState) EnvPrefix() string {
	return "ASTRONA_CLUSTER_" + envName(l.Name)
}

// legacyEnvPrefix is the pre-v0.3 prefix (ASTRONA_LINK_IDP), still
// published so labs written for it keep working.
func (l LinkState) legacyEnvPrefix() string {
	return "ASTRONA_LINK_" + envName(l.Name)
}

func envName(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// LinkEnv is the environment that tells a lab's scripts and command checks
// where its linked clusters are.
func LinkEnv(links []LinkState) []string {
	var env []string
	for _, l := range links {
		kc := ExistingKubeconfig(l.Cluster)
		for _, p := range []string{l.EnvPrefix(), l.legacyEnvPrefix()} {
			env = append(env, p+"_HOST="+l.Host(), p+"_HOSTNAME="+l.Hostname(), p+"_CONTEXT="+l.Context())
			if kc != "" {
				env = append(env, p+"_KUBECONFIG="+kc)
			}
		}
	}
	return env
}

func linksPath(lab string) (string, error) {
	dir, err := labStateDir(lab)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "links.json"), nil
}

// WriteLinks saves lab's resolved links (empty removes them).
func WriteLinks(lab string, links []LinkState) error {
	path, err := linksPath(lab)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(links, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0600)
}

// ReadLinks returns lab's saved links (none if it has no links file).
func ReadLinks(lab string) ([]LinkState, error) {
	path, err := linksPath(lab)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var links []LinkState
	if err := json.Unmarshal(data, &links); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return links, nil
}
