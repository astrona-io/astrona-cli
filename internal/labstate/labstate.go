// Package labstate keeps what astrona remembers about one running lab
// between commands, in ~/.astrona/labs/<lab>.json (0600 in a 0700 dir):
// where its config came from (so `astrona submit` can grade the only
// running lab without being told which), the kubectl context the user had
// before `astrona run` switched to the lab (so `astrona destroy` can put it
// back), and the Astrona lab session it was started under (so a result can
// be sent to the lab page).
//
// No secret is kept here: the lab session's own token is never stored, and
// nothing of the CLI sign-in is.
package labstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"astrona/internal/config"
)

// Source is where the lab's config came from: the same inputs
// -c/--file/--git/--git-ref take, plus the catalog name when it was
// started by one.
type Source struct {
	Config  string `json:"config"`
	File    string `json:"file,omitempty"`
	Git     string `json:"git,omitempty"`
	GitRef  string `json:"gitRef,omitempty"`
	Catalog string `json:"catalog,omitempty"`
}

// KubeContext is the switch `astrona run` made: Lab is the context it made
// current, Previous the one that was current before ("" with
// PreviousUnset when there was none).
type KubeContext struct {
	Lab           string `json:"lab"`
	Previous      string `json:"previous,omitempty"`
	PreviousUnset bool   `json:"previousUnset,omitempty"`
}

// Session is the Astrona lab session the lab was started under. Its
// token is deliberately not kept.
type Session struct {
	Site      string `json:"site"`
	ID        string `json:"sessionId"`
	Lab       string `json:"lab"`
	URL       string `json:"url,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	// MaxMinutes is the attempt's time limit (0: not given).
	MaxMinutes int `json:"maxMinutes,omitempty"`
}

// State is everything remembered about one lab.
type State struct {
	Source      *Source      `json:"source,omitempty"`
	KubeContext *KubeContext `json:"kubeContext,omitempty"`
	Session     *Session     `json:"session,omitempty"`
}

func (s *State) empty() bool {
	return s == nil || (s.Source == nil && s.KubeContext == nil && s.Session == nil)
}

// Dir returns ~/.astrona/labs (not created).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".astrona", "labs"), nil
}

// Path is lab's state file. The name is validated so it can't escape
// ~/.astrona/labs.
func Path(lab string) (string, error) {
	if err := config.ValidateName(lab); err != nil {
		return "", fmt.Errorf("lab name: %w", err)
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, lab+".json"), nil
}

// Load returns lab's state, or nil when nothing is remembered.
func Load(lab string) (*State, error) {
	path, err := Path(lab)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lab state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// Save writes lab's state (atomically, 0600). An empty state removes the
// file.
func Save(lab string, s *State) error {
	if s.empty() {
		return Remove(lab)
	}
	path, err := Path(lab)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// Update loads lab's state (empty when none), applies change and saves it.
func Update(lab string, change func(*State)) error {
	s, err := Load(lab)
	if err != nil {
		return err
	}
	if s == nil {
		s = &State{}
	}
	change(s)
	return Save(lab, s)
}

// Remove forgets lab. Missing is fine.
func Remove(lab string) error {
	path, err := Path(lab)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove lab state: %w", err)
	}
	return nil
}

// Names lists the labs with remembered state, sorted.
func Names() ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || config.ValidateName(name) != nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
