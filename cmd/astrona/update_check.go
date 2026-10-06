package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mattn/go-isatty"
)

const (
	// updateCheckEvery is how often GitHub is asked for the latest release;
	// updateRetryAfter is the wait after a failed check (offline).
	updateCheckEvery = 24 * time.Hour
	updateRetryAfter = time.Hour
	// updateNoticeEvery is how often the "new version" notice is shown.
	updateNoticeEvery = 24 * time.Hour
)

// updateState is ~/.astrona/update-check.json.
type updateState struct {
	CheckedAt  time.Time `json:"checkedAt"`
	Failed     bool      `json:"failed,omitempty"`
	Latest     string    `json:"latest,omitempty"`
	NotifiedAt time.Time `json:"notifiedAt"`
}

func (s updateState) dueForCheck(now time.Time) bool {
	wait := updateCheckEvery
	if s.Failed {
		wait = updateRetryAfter
	}
	return now.Sub(s.CheckedAt) >= wait
}

func (s updateState) dueForNotice(current string, now time.Time) bool {
	return newerRelease(s.Latest, current) && now.Sub(s.NotifiedAt) >= updateNoticeEvery
}

// updateCheckWanted: never in CI, never when nobody sees stderr, never
// inside a hand-over to another version, and never when switched off.
func updateCheckWanted() bool {
	for _, v := range []string{"ASTRONA_NO_UPDATE_CHECK", "CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", dispatchedEnv} {
		if os.Getenv(v) != "" {
			return false
		}
	}
	return isatty.IsTerminal(os.Stderr.Fd())
}

func updateStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".astrona", "update-check.json"), nil
}

func loadUpdateState(path string) updateState {
	var s updateState
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// saveUpdateState is best effort — a read-only home just means checking
// again next time.
func saveUpdateState(path string, s updateState) {
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0700) == nil {
		_ = os.WriteFile(path, data, 0600)
	}
}
