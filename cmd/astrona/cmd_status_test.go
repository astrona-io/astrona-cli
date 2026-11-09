package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"astrona/internal/exam"
	"astrona/internal/labops"
	"astrona/internal/proctor"
)

func TestPrintLabStatus(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	st := labops.LabStatus{
		Row:      labops.LabRow{Name: "astro-x", Runtime: "kind", Status: "Ready (2/2)", Version: "v1.37.0", Uptime: "20m"},
		Exam:     &exam.State{StartedAt: now.Add(-30 * time.Minute), Limit: 2 * time.Hour},
		Attempts: []proctor.Attempt{{Time: now.Add(-5 * time.Minute), Earned: 3, Max: 4}},
		Now:      now,
	}
	var buf bytes.Buffer
	printLabStatus(&buf, st)
	for _, want := range []string{
		"Lab astro-x (kind)",
		"Ready (2/2) · Kubernetes v1.37.0 · up 20m",
		"astrona shell astro-x",
		"30m0s of 2h0m0s used (1h30m0s left)",
		"3/4 points (75%) FAIL · 5m0s ago · attempt #1",
		"Next: astrona submit --watch for live feedback while you fix it — or astrona reset --soft to start over",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q:\n%s", want, buf.String())
		}
	}

	buf.Reset()
	printLabStatus(&buf, labops.LabStatus{Row: labops.LabRow{Name: "astro-vm", Runtime: "qemu", Status: "Running"}, Now: now})
	for _, want := range []string{"astrona ssh astro-vm", "Last submit  none yet"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("qemu status missing %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "Exam") {
		t.Error("non-exam lab shows an Exam line")
	}
}
