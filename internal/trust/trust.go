// Package trust remembers which remote lab sources the user has approved
// to run on their machine. A lab fetched with --git or from a URL runs its
// scripts with bash on the host; the first time a source is used — and
// again whenever its content changes — the user is shown what it will do
// and asked (trust-on-first-use, pinned to a git commit or the config's
// SHA-256). Approvals live in ~/.astrona/trust.json.
package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Source identifies remote lab content and pins its exact version.
type Source struct {
	Kind     string // "git" or "url"
	Location string // repo URL (+ subdir) or config URL
	Pin      string // git commit, or sha256 of the config
}

func (s Source) key() string { return s.Kind + ":" + s.Location }

// Record is one approval.
type Record struct {
	Pin        string    `json:"pin"`
	ApprovedAt time.Time `json:"approvedAt"`
}

// Status is a source's standing against the store.
type Status int

const (
	// New: never approved.
	New Status = iota
	// Changed: approved before, but at a different pin.
	Changed
	// Trusted: approved at exactly this pin.
	Trusted
)

func storePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".astrona", "trust.json"), nil
}

func load() (map[string]Record, error) {
	path, err := storePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Record{}, nil
		}
		return nil, fmt.Errorf("read trust store: %w", err)
	}
	records := map[string]Record{}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse trust store %s: %w", path, err)
	}
	return records, nil
}

// Check reports src's status and, for Changed, the previously approved pin.
func Check(src Source) (Status, string, error) {
	records, err := load()
	if err != nil {
		return New, "", err
	}
	r, ok := records[src.key()]
	switch {
	case !ok:
		return New, "", nil
	case r.Pin != src.Pin:
		return Changed, r.Pin, nil
	default:
		return Trusted, r.Pin, nil
	}
}

// Approve records src as trusted at its current pin.
func Approve(src Source, now time.Time) error {
	records, err := load()
	if err != nil {
		return err
	}
	records[src.key()] = Record{Pin: src.Pin, ApprovedAt: now}
	path, err := storePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".trust-*.json")
	if err != nil {
		return fmt.Errorf("write trust store: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write trust store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write trust store: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return fmt.Errorf("write trust store: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}
