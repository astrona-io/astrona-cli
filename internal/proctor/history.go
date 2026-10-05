package proctor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Attempt is one `astrona submit` as recorded in the lab's history.
type Attempt struct {
	Time   time.Time      `json:"time"`
	Pass   bool           `json:"pass"`
	Earned int            `json:"earned"`
	Max    int            `json:"max"`
	Checks []AttemptCheck `json:"checks"`
}

type AttemptCheck struct {
	Name string `json:"name"`
	Pass bool   `json:"pass"`
}

// NewAttempt summarizes a graded submission.
func NewAttempt(results []CheckResult, pass bool, at time.Time) Attempt {
	s := ScoreOf(results)
	a := Attempt{Time: at, Pass: pass, Earned: s.Earned, Max: s.Max}
	for _, r := range results {
		a.Checks = append(a.Checks, AttemptCheck{Name: r.Name, Pass: r.Pass})
	}
	return a
}

var historyLabPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// historyPath is ~/.astrona/results/<lab>.jsonl — one JSON attempt per
// line, append-only.
func historyPath(lab string) (string, error) {
	if !historyLabPattern.MatchString(lab) {
		return "", fmt.Errorf("invalid lab name '%s' for result history", lab)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".astrona", "results", lab+".jsonl"), nil
}

// RecordAttempt appends a to lab's history.
func RecordAttempt(lab string, a Attempt) error {
	path, err := historyPath(lab)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create results dir: %w", err)
	}
	line, err := json.Marshal(a)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open result history: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write result history: %w", err)
	}
	return nil
}

// LoadAttempts reads lab's history, oldest first. Corrupt lines are
// skipped rather than hiding the rest. No history is not an error.
func LoadAttempts(lab string) ([]Attempt, error) {
	path, err := historyPath(lab)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open result history: %w", err)
	}
	defer f.Close()

	var out []Attempt
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var a Attempt
		if json.Unmarshal(sc.Bytes(), &a) == nil {
			out = append(out, a)
		}
	}
	return out, sc.Err()
}
