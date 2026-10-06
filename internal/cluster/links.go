package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LinkState is a resolved link of a running lab: the link's name and the
// kind cluster it points at. Saved at `astrona run` time in
// ~/.astrona/kind/<lab>/links.json, so later commands (submit, shell,
// status) know a lab's links without needing its config.
type LinkState struct {
	Name    string `json:"name"`
	Cluster string `json:"cluster"`
}

// Host is the linked cluster's control-plane container name — resolvable
// from any kind node (and so any pod) on the shared container network.
func (l LinkState) Host() string { return l.Cluster + "-control-plane" }

// Context is the linked cluster's kube context name.
func (l LinkState) Context() string { return "kind-" + l.Cluster }

// EnvPrefix mirrors config.Link.EnvPrefix.
func (l LinkState) EnvPrefix() string {
	return "ASTRONA_LINK_" + strings.ToUpper(strings.ReplaceAll(l.Name, "-", "_"))
}

// LinkEnv is the environment that tells a lab's scripts and command checks
// where its linked labs are.
func LinkEnv(links []LinkState) []string {
	var env []string
	for _, l := range links {
		p := l.EnvPrefix()
		env = append(env, p+"_HOST="+l.Host(), p+"_CONTEXT="+l.Context())
		if kc := ExistingKubeconfig(l.Cluster); kc != "" {
			env = append(env, p+"_KUBECONFIG="+kc)
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
