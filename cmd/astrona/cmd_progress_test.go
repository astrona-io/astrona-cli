package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"astrona/internal/proctor"
)

func TestSummarizeProgress(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	attempts := []proctor.Attempt{
		{Time: t0, Earned: 1, Max: 4},
		{Time: t0.Add(time.Hour), Earned: 4, Max: 4, Pass: true, Timed: true, ElapsedSeconds: 1500},
		{Time: t0.Add(2 * time.Hour), Earned: 3, Max: 4},
		{Time: t0.Add(3 * time.Hour), Earned: 4, Max: 4, Pass: true, Timed: true, ElapsedSeconds: 900},
	}
	p := proctor.SummarizeProgress("astro-web", attempts)
	if p.Attempts != 4 || !p.Passed || p.PassedAt != 2 || p.BestEarned != 4 || p.FastestPassSeconds != 900 || !p.LastAttempt.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("progress = %+v", p)
	}
	never := proctor.SummarizeProgress("astro-db", []proctor.Attempt{{Earned: 0, Max: 2}, {Earned: 1, Max: 2}})
	if never.Passed || never.BestEarned != 1 || never.FastestPassSeconds != 0 {
		t.Fatalf("never passed = %+v", never)
	}
}

func TestPrintProgressTable(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	printProgressTable(&buf, []proctor.LabProgress{
		{Lab: "astro-web", Attempts: 4, Passed: true, PassedAt: 2, BestEarned: 4, BestMax: 4, LastAttempt: now.Add(-3 * time.Hour), FastestPassSeconds: 900},
		{Lab: "astro-db", Attempts: 2, BestEarned: 1, BestMax: 2, LastAttempt: now.Add(-50 * time.Hour)},
	}, now)
	for _, want := range []string{"LAB", "web", "passed (#2)", "4/4 (100%)", "15m0s", "3h ago", "db", "not yet", "1/2 (50%)", "2d ago", "1 of 2 lab(s) passed."} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	printProgressTable(&buf, nil, now)
	if !strings.Contains(buf.String(), "No results yet") {
		t.Errorf("empty = %q", buf.String())
	}
	if humanAgo(30*time.Second) != "just now" || humanAgo(12*time.Minute) != "12m ago" {
		t.Error("humanAgo wrong")
	}
}

func TestHistoryLabs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if labs, err := proctor.HistoryLabs(); err != nil || labs != nil {
		t.Fatalf("no results dir = %v, %v", labs, err)
	}
	proctor.RecordAttempt("astro-b", proctor.Attempt{Time: time.Now()})
	proctor.RecordAttempt("astro-a", proctor.Attempt{Time: time.Now()})
	labs, err := proctor.HistoryLabs()
	if err != nil || strings.Join(labs, ",") != "astro-a,astro-b" {
		t.Fatalf("labs = %v, %v", labs, err)
	}
}
