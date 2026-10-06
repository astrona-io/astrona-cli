package main

import (
	"strings"
	"testing"
	"time"

	"astrona/internal/exam"
	"astrona/internal/proctor"
)

func TestRenderWatchBoard(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	results := []proctor.CheckResult{
		{Name: "namespace", Pass: true, Points: 3},
		{Name: "configmap", Pass: false, Points: 1, Hint: "use -n shop"},
	}
	ex := &exam.State{StartedAt: now.Add(-10 * time.Minute), Limit: time.Hour}

	got := renderWatchBoard(results, true, 75, true, ex, now)
	for _, want := range []string{"✓ namespace", "✗ configmap", "hint: use -n shop", "Score: 3/4 (75%) — pass mark 75%", "Time: 10m0s of 1h0m0s used", "Passing — run `astrona submit`"} {
		if !strings.Contains(got, want) {
			t.Errorf("board missing %q:\n%s", want, got)
		}
	}
	got = renderWatchBoard(results, false, 0, false, nil, now)
	if strings.Contains(got, "hint:") || strings.Contains(got, "Time:") || !strings.Contains(got, "Not passing yet") {
		t.Errorf("board without hints/exam:\n%s", got)
	}
}

func TestWatchKeyChangesOnlyWithResults(t *testing.T) {
	a := []proctor.CheckResult{{Name: "x", Pass: false, Duration: time.Second}}
	b := []proctor.CheckResult{{Name: "x", Pass: false, Duration: 2 * time.Second, Message: "other"}}
	c := []proctor.CheckResult{{Name: "x", Pass: true}}
	if watchKey(a) != watchKey(b) {
		t.Error("timing/message differences must not count as a change")
	}
	if watchKey(a) == watchKey(c) {
		t.Error("a check flipping must count as a change")
	}
}
