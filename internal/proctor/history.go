package proctor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Attempt is one `astrona submit` as recorded in the lab's history.
type Attempt struct {
	Time   time.Time      `json:"time"`
	Pass   bool           `json:"pass"`
	Earned int            `json:"earned"`
	Max    int            `json:"max"`
	Checks []AttemptCheck `json:"checks"`
	// Exam labs only: time used when submitting, and whether that was
	// past the limit.
	Timed          bool  `json:"timed,omitempty"`
	ElapsedSeconds int64 `json:"elapsedSeconds,omitempty"`
	OverTime       bool  `json:"overTime,omitempty"`
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

// HistoryLabs lists every lab with a recorded attempt history, sorted.
func HistoryLabs() ([]string, error) {
	path, err := historyPath("x")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read results dir: %w", err)
	}
	var labs []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if ok && !e.IsDir() && historyLabPattern.MatchString(name) {
			labs = append(labs, name)
		}
	}
	sort.Strings(labs)
	return labs, nil
}

// LabProgress summarizes one lab's attempt history.
type LabProgress struct {
	Lab         string    `json:"lab"`
	Attempts    int       `json:"attempts"`
	Passed      bool      `json:"passed"`
	PassedAt    int       `json:"passedOnAttempt,omitempty"` // first passing attempt (1-based)
	BestEarned  int       `json:"bestEarned"`
	BestMax     int       `json:"bestMax"`
	LastAttempt time.Time `json:"lastAttempt"`
	// FastestPass is the least exam time used by a passing attempt
	// (seconds; exam labs only).
	FastestPassSeconds int64 `json:"fastestPassSeconds,omitempty"`
}

// BestPercent is the best attempt's score as 0–100.
func (p LabProgress) BestPercent() float64 {
	if p.BestMax == 0 {
		return 100
	}
	return 100 * float64(p.BestEarned) / float64(p.BestMax)
}

// SummarizeProgress folds attempts (oldest first) into a LabProgress.
func SummarizeProgress(lab string, attempts []Attempt) LabProgress {
	p := LabProgress{Lab: lab, Attempts: len(attempts)}
	bestPct := -1.0
	for i, a := range attempts {
		pct := 100.0
		if a.Max > 0 {
			pct = 100 * float64(a.Earned) / float64(a.Max)
		}
		if pct > bestPct {
			bestPct, p.BestEarned, p.BestMax = pct, a.Earned, a.Max
		}
		if a.Pass {
			if !p.Passed {
				p.Passed, p.PassedAt = true, i+1
			}
			if a.Timed && (p.FastestPassSeconds == 0 || a.ElapsedSeconds < p.FastestPassSeconds) {
				p.FastestPassSeconds = max(a.ElapsedSeconds, 1)
			}
		}
		if a.Time.After(p.LastAttempt) {
			p.LastAttempt = a.Time
		}
	}
	return p
}
