package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/proctor"
)

func TestPrintProgressAndAttempts(t *testing.T) {
	a1 := proctor.Attempt{Time: time.Now(), Earned: 1, Max: 3, Checks: []proctor.AttemptCheck{{Name: "svc", Pass: false}, {Name: "pod", Pass: true}}}
	a2 := proctor.Attempt{Time: time.Now(), Earned: 3, Max: 3, Pass: true, Checks: []proctor.AttemptCheck{{Name: "svc", Pass: true}, {Name: "pod", Pass: true}}}

	var buf bytes.Buffer
	printProgress(&buf, nil, a1)
	if !strings.Contains(buf.String(), "Attempt #1 recorded") {
		t.Errorf("first = %q", buf.String())
	}
	buf.Reset()
	printProgress(&buf, []proctor.Attempt{a1}, a2)
	for _, want := range []string{"Attempt #2: 3/3 (+2 point(s) since #1)", "now passing: svc"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("progress missing %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "pod") {
		t.Error("unchanged check reported")
	}

	buf.Reset()
	printAttempts(&buf, "astro-x", []proctor.Attempt{a1, a2})
	for _, want := range []string{"#", "1/3", "FAIL", "3/3", "PASS", "Best: 3/3 · 2 attempt(s)"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("history missing %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	printAttempts(&buf, "astro-x", nil)
	if !strings.Contains(buf.String(), "No attempts recorded") {
		t.Errorf("empty = %q", buf.String())
	}
}

func TestPrintAttemptsTimed(t *testing.T) {
	var buf bytes.Buffer
	printAttempts(&buf, "astro-x", []proctor.Attempt{
		{Time: time.Now(), Earned: 1, Max: 4, Timed: true, ElapsedSeconds: 0},
		{Time: time.Now(), Earned: 2, Max: 4, Timed: true, ElapsedSeconds: 1800},
		{Time: time.Now(), Earned: 4, Max: 4, Pass: false, Timed: true, ElapsedSeconds: 66, OverTime: true},
	})
	for _, want := range []string{"TIME USED", "0s", "30m0s", "1m6s (over)"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q:\n%s", want, buf.String())
		}
	}
}

// An invalid lab config fails `submit` and `test` up front as a plain
// error (exit 1) — never graded into a "didn't pass" (exit 2).
func TestSubmitAndTestRejectInvalidConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // nothing real gets run, even if validation regressed
	dir := t.TempDir()
	cfg := "metadata: {name: bad-check}\nvalidation:\n  checks:\n    - {name: pods, type: count, resource: pods}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"submit", "test"} {
		t.Run(command, func(t *testing.T) {
			root := newRootCmd(&rootFlags{})
			root.SetArgs([]string{command, "-c", dir})
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "count check needs min and/or max") {
				t.Fatalf("error = %v, want the config validation error", err)
			}
			if code := exitCodeFor(err); code != exitError {
				t.Fatalf("exit code = %d, want %d", code, exitError)
			}
		})
	}
}
