package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/proctor"
)

func TestPrintLabStatus(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	st := labStatus{
		row:      labRow{name: "astro-x", runtime: "kind", status: "Ready (2/2)", version: "v1.37.0", uptime: "20m"},
		exam:     &exam.State{StartedAt: now.Add(-30 * time.Minute), Limit: 2 * time.Hour},
		attempts: []proctor.Attempt{{Time: now.Add(-5 * time.Minute), Earned: 3, Max: 4}},
		now:      now,
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
	printLabStatus(&buf, labStatus{row: labRow{name: "astro-vm", runtime: "qemu", status: "Running"}, now: now})
	for _, want := range []string{"astrona ssh astro-vm", "Last submit  none yet"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("qemu status missing %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "Exam") {
		t.Error("non-exam lab shows an Exam line")
	}
}

func TestNextStep(t *testing.T) {
	now := time.Now()
	withTask := &config.LabConfig{Metadata: config.MetadataConfig{Docs: config.DocsConfig{Question: "q.md"}}}
	cases := []struct {
		st   labStatus
		want string
	}{
		{labStatus{cfg: withTask, now: now}, "astrona docs question"},
		{labStatus{exam: &exam.State{StartedAt: now.Add(-3 * time.Hour), Limit: time.Hour}, attempts: []proctor.Attempt{{}}, now: now}, "time is up"},
		{labStatus{attempts: []proctor.Attempt{{Pass: true}}, now: now}, "passed"},
		{labStatus{row: labRow{name: "astro-x", status: "Stopped"}, now: now}, "astrona start astro-x"},
		{labStatus{row: labRow{status: "Ready (1/1)"}, now: now}, "astrona submit"},
	}
	for _, c := range cases {
		if got := nextStep(c.st); !strings.Contains(got, c.want) {
			t.Errorf("nextStep = %q, want containing %q", got, c.want)
		}
	}
}
