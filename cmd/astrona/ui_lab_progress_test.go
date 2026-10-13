package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestLabTaskEstimate(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        time.Duration
	}{
		{"minutes", "20m", 20 * time.Minute},
		{"combined", "1h30m", 90 * time.Minute},
		{"quoted", `" 45m "`, 45 * time.Minute},
		{"missing", "", 0},
		{"invalid", "about an hour", 0},
		{"zero", "0m", 0},
		{"negative", "-20m", 0},
		{"overflow", "999999999999h", 0},
		{"number without unit", "20", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, expected := labTaskDocument("---\ntitle: Task\nestimated_duration: " + tc.value + "\n---\nUse kubectl\x1b[2J")
			if expected != tc.want || text != "# Task\n\nUse kubectl[2J" {
				t.Fatalf("task=%q estimate=%s, want %s", text, expected, tc.want)
			}
		})
	}
	text, expected := labTaskDocument("# Task\nNo estimate")
	if text != "# Task\nNo estimate" || expected != 0 {
		t.Fatalf("plain task changed: %q %s", text, expected)
	}
}

func TestLabTimeProgress(t *testing.T) {
	for _, tc := range []struct {
		name     string
		used     time.Duration
		expected time.Duration
		want     string
	}{
		{"start", 0, 20 * time.Minute, "[────────────────] Used 0s / Expected 20m0s"},
		{"half", 10 * time.Minute, 20 * time.Minute, "[━━━━━━━━────────] Used 10m0s / Expected 20m0s"},
		{"complete", 20 * time.Minute, 20 * time.Minute, "[━━━━━━━━━━━━━━━━] Used 20m0s / Expected 20m0s"},
		{"overtime", 25 * time.Minute, 20 * time.Minute, "[━━━━━━━━━━━━━━━━] Used 25m0s / Expected 20m0s · +5m0s over estimate"},
		{"unknown", time.Minute, 0, "Used 1m /"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := uiPaneModel{title: "TASK / lab", labProgress: uiLabProgress{Lab: true, Expected: tc.expected}, activityTotal: tc.used}
			m.activity.target.Lab = "lab"
			got := ansi.Strip(m.labTimeProgress())
			if tc.expected == 0 {
				if !strings.Contains(got, "Expected time not provided") || strings.Contains(got, "[") {
					t.Fatal(got)
				}
			} else if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			m.activity.paused = true
			if !strings.HasSuffix(m.labTimeProgress(), " · Paused") {
				t.Fatal("paused state missing")
			}
			m.labProgress.Lab = false
			if m.labTimeProgress() != "" {
				t.Fatal("playground shows lab progress")
			}
			m.labProgress.Lab, m.title = true, "TASK / another-lab"
			if m.labTimeProgress() != "" {
				t.Fatal("another lab's estimate leaked into this task")
			}
			m.title = "HOME"
			if m.labTimeProgress() != "" {
				t.Fatal("home shows lab progress")
			}
		})
	}
}

func TestLabProgressUsesPausedActivityClock(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	m := activityTestPane(t, start)
	t.Setenv(uiActivityEnv, m.activityPath)
	stop := uiActivityStart("astro-lab", start.Format(time.RFC3339), uiLabProgress{Lab: true, Expected: 20 * time.Minute})
	defer stop()
	m.activityTick(start)
	if !m.labProgress.Lab || m.labProgress.Expected != 20*time.Minute {
		t.Fatal("private activity file did not carry the task estimate")
	}
	m.activity.input(start.Add(time.Minute))
	m.activityTick(start.Add(4 * time.Minute))
	if !m.activity.paused || m.timeSpent() != time.Minute {
		t.Fatal("idle time counted towards progress")
	}
	before := m.labTimeProgress()
	m.activityTick(start.Add(20 * time.Minute))
	if m.labTimeProgress() != before || !strings.Contains(before, "Paused") {
		t.Fatal("progress advanced during the idle dialog")
	}
	for _, size := range [][2]int{{120, 35}, {80, 24}, {60, 20}} {
		m.width, m.height = size[0], size[1]
		m.resizeContent()
		if !strings.Contains(ansi.Strip(m.View()), "Expected 20m") {
			t.Fatalf("progress missing at %v", size)
		}
	}
	// Display metadata must disappear if the saved run no longer matches.
	data, err := json.Marshal(uiActivityRecord{uiActivityTarget{"astro-lab", "stale-run", "other"}, m.labProgress})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.activityPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	m.activityTick(start.Add(21 * time.Minute))
	if m.labTimeProgress() != "" {
		t.Fatal("stale run still displays progress")
	}
}
