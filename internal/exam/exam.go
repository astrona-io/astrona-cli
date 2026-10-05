// Package exam keeps a timed lab's clock: when the attempt started and
// how long it may take, in ~/.astrona/exams/<lab>.json, so `submit` and
// `status` (separate processes from the `run` that started it) can tell
// how much time is left.
package exam

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// State is one running exam attempt.
type State struct {
	StartedAt time.Time     `json:"startedAt"`
	Limit     time.Duration `json:"limit"`
}

// Elapsed / Remaining / Over are relative to now.
func (s State) Elapsed(now time.Time) time.Duration { return now.Sub(s.StartedAt) }

func (s State) Remaining(now time.Time) time.Duration { return s.Limit - s.Elapsed(now) }

func (s State) Over(now time.Time) bool { return s.Remaining(now) < 0 }

// Summary is e.g. "1h12m of 2h0m used (48m left)" or "over time by 5m".
func (s State) Summary(now time.Time) string {
	if s.Over(now) {
		return fmt.Sprintf("over time by %s (limit %s)", Round(-s.Remaining(now)), Round(s.Limit))
	}
	return fmt.Sprintf("%s of %s used (%s left)", Round(s.Elapsed(now)), Round(s.Limit), Round(s.Remaining(now)))
}

// Round renders d to the second under 10 minutes (where seconds matter
// — "over time by 6s"), to the minute above.
func Round(d time.Duration) string {
	if d < 10*time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

var labPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func statePath(lab string) (string, error) {
	if !labPattern.MatchString(lab) {
		return "", fmt.Errorf("invalid lab name '%s' for exam state", lab)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".astrona", "exams", lab+".json"), nil
}

// Start (re)starts lab's exam clock at now.
func Start(lab string, limit time.Duration, now time.Time) error {
	path, err := statePath(lab)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create exam state dir: %w", err)
	}
	data, err := json.Marshal(State{StartedAt: now, Limit: limit})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Load returns lab's running exam, or nil if it has none.
func Load(lab string) (*State, error) {
	path, err := statePath(lab)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read exam state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse exam state: %w", err)
	}
	return &s, nil
}

// Clear ends lab's exam (destroy). Missing is fine.
func Clear(lab string) error {
	path, err := statePath(lab)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove exam state: %w", err)
	}
	return nil
}
