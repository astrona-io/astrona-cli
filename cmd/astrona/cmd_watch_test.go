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

	got := renderWatchBoard(results, true, 75, true, ex, false, now)
	for _, want := range []string{"✓ namespace", "✗ configmap", "hint: use -n shop", "Score: 3/4 (75%) — pass mark 75%", "Time: 10m0s of 1h0m0s used", "Passing — run `astrona submit`"} {
		if !strings.Contains(got, want) {
			t.Errorf("board missing %q:\n%s", want, got)
		}
	}
	got = renderWatchBoard(results, false, 0, false, nil, false, now)
	if strings.Contains(got, "hint:") || strings.Contains(got, "Time:") || !strings.Contains(got, "Not passing yet") {
		t.Errorf("board without hints/exam:\n%s", got)
	}
}

// Past the time limit with exam.strict, a passing board must say what a
// submit would record — not "Passing".
func TestRenderWatchBoardStrictOverTime(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	results := []proctor.CheckResult{{Name: "namespace", Pass: true, Points: 1}}
	over := &exam.State{StartedAt: now.Add(-2 * time.Hour), Limit: time.Hour}
	inTime := &exam.State{StartedAt: now.Add(-10 * time.Minute), Limit: time.Hour}

	tests := []struct {
		name    string
		ex      *exam.State
		strict  bool
		want    string
		mustNot string
	}{
		{"strict over time", over, true, "would not count as a pass (exam.strict)", "Passing"},
		{"not strict over time", over, false, "Passing", "exam.strict"},
		{"strict in time", inTime, true, "Passing", "exam.strict"},
		{"strict no exam state", nil, true, "Passing", "exam.strict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderWatchBoard(results, true, 0, true, tt.ex, tt.strict, now)
			if !strings.Contains(got, tt.want) || strings.Contains(got, tt.mustNot) {
				t.Errorf("board should contain %q and not %q:\n%s", tt.want, tt.mustNot, got)
			}
		})
	}
}

func TestCheckWatchFlags(t *testing.T) {
	tests := []struct {
		name    string
		watch   bool
		output  string
		junit   string
		history bool
		keep    bool
		wantErr string
	}{
		{name: "watch alone", watch: true},
		{name: "no watch, everything else", output: "json", junit: "r.xml", history: true, keep: true},
		{name: "json", watch: true, output: "json", wantErr: "-o json"},
		{name: "junit", watch: true, junit: "r.xml", wantErr: "--junit-xml"},
		{name: "history", watch: true, history: true, wantErr: "--history"},
		{name: "keep", watch: true, keep: true, wantErr: "--keep"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkWatchFlags(tt.watch, tt.output, tt.junit, tt.history, tt.keep)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one naming %s", err, tt.wantErr)
			}
		})
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
