package proctor

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/runtime"
)

func TestScoreAndPassRule(t *testing.T) {
	results := []CheckResult{
		{Name: "a", Pass: true, Points: 3},
		{Name: "b", Pass: false, Points: 1},
		{Name: "c", Pass: true}, // unset points count as 1
	}
	s := ScoreOf(results)
	if s.Earned != 4 || s.Max != 5 || s.Percent() != 80 {
		t.Fatalf("score = %+v (%.0f%%)", s, s.Percent())
	}
	if Passed(results, 0) {
		t.Error("without passPercent every check must pass")
	}
	if !Passed(results, 80) || Passed(results, 81) {
		t.Error("passPercent boundary wrong")
	}
	if (Score{}).Percent() != 100 || !Passed(nil, 0) {
		t.Error("empty lab should pass")
	}
}

// captureStdout runs fn and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestGradeShowsHintsAndScore(t *testing.T) {
	// runChecks requires kubectl on PATH even for command checks.
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\nexit 0\n"), 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.LabConfig{Validation: config.ValidationConfig{
		PassPercent: 60,
		Checks: []config.ValidationCheck{
			{Name: "service exists", Type: "command", Command: "true", Points: 3},
			{Name: "selector matches", Type: "command", Command: "false", Hint: "compare the Service selector with the pod labels", Points: 2},
		},
	}}
	env := &runtime.LabEnvironment{Executor: executor.LocalExecutor{}}

	var pass bool
	out := captureStdout(t, func() {
		var err error
		_, pass, err = NewProctor(t.TempDir(), env).Grade(cfg)
		if err != nil {
			t.Fatal(err)
		}
	})
	if !pass {
		t.Error("3/5 = 60% should pass a 60% pass mark")
	}
	for _, want := range []string{"FAIL  selector matches", "hint: compare the Service selector", "Score: 3/5 points (60%) — pass mark 60%"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	p := NewProctor(t.TempDir(), env)
	p.HideHints()
	out = captureStdout(t, func() { p.Grade(cfg) })
	if strings.Contains(out, "hint:") {
		t.Error("HideHints still printed a hint")
	}
}

func TestAttemptHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if a, err := LoadAttempts("astro-x"); err != nil || a != nil {
		t.Fatalf("no history = %v, %v", a, err)
	}
	r1 := []CheckResult{{Name: "a", Pass: false}, {Name: "b", Pass: true}}
	r2 := []CheckResult{{Name: "a", Pass: true}, {Name: "b", Pass: true}}
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if err := RecordAttempt("astro-x", NewAttempt(r1, false, t0)); err != nil {
		t.Fatal(err)
	}
	if err := RecordAttempt("astro-x", NewAttempt(r2, true, t0.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}

	got, err := LoadAttempts("astro-x")
	if err != nil || len(got) != 2 {
		t.Fatalf("LoadAttempts = %v, %v", got, err)
	}
	if got[0].Pass || !got[1].Pass || got[1].Earned != 2 || got[1].Checks[0].Name != "a" {
		t.Errorf("attempts = %+v", got)
	}

	path, _ := historyPath("astro-x")
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Errorf("history mode = %o", info.Mode().Perm())
	}
	if _, err := historyPath("../x"); err == nil {
		t.Error("unsafe lab name accepted")
	}
}

func TestValidateScoring(t *testing.T) {
	bad := []*config.LabConfig{
		{Validation: config.ValidationConfig{PassPercent: 101}},
		{Validation: config.ValidationConfig{Checks: []config.ValidationCheck{{Points: -1}}}},
		{Validation: config.ValidationConfig{Script: &config.ResourceItem{Name: "s", Points: -2}}},
	}
	for i, c := range bad {
		if config.ValidateScoring(c) == nil {
			t.Errorf("bad[%d] accepted", i)
		}
	}
	if err := config.ValidateScoring(&config.LabConfig{Validation: config.ValidationConfig{PassPercent: 66}}); err != nil {
		t.Error(err)
	}
}

func TestQuietShortensPodReadyAndSilencesScripts(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\necho \"$*\" >> "+log+"\n"), 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	env := &runtime.LabEnvironment{KubeContext: "kind-x", Executor: executor.LocalExecutor{}}
	checks := []config.ValidationCheck{{Name: "pods", Type: "podReady", Resource: "pod/x"}}

	p := NewProctor(t.TempDir(), env)
	p.runChecks(checks)
	p.Quiet()
	p.runChecks(checks)

	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "--timeout=60s") || !strings.Contains(string(calls), "--timeout=2s") {
		t.Fatalf("podReady timeouts = %s", calls)
	}
	if p.scriptOut != io.Discard {
		t.Error("Quiet should discard script output")
	}
}
