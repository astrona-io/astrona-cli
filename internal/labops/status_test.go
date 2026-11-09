package labops

import (
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/proctor"
)

func TestNextStep(t *testing.T) {
	now := time.Now()
	withTask := &config.LabConfig{Metadata: config.MetadataConfig{Docs: config.DocsConfig{Question: "q.md"}}}
	cases := []struct {
		st   LabStatus
		want string
	}{
		{LabStatus{Cfg: withTask, Now: now}, "astrona docs question"},
		{LabStatus{Exam: &exam.State{StartedAt: now.Add(-3 * time.Hour), Limit: time.Hour}, Attempts: []proctor.Attempt{{}}, Now: now}, "time is up"},
		{LabStatus{Attempts: []proctor.Attempt{{Pass: true}}, Now: now}, "passed"},
		{LabStatus{Row: LabRow{Name: "astro-x", Status: "Stopped"}, Now: now}, "astrona start astro-x"},
		{LabStatus{Row: LabRow{Status: "Ready (1/1)"}, Now: now}, "astrona submit"},
	}
	for _, c := range cases {
		if got := NextStep(c.st); !strings.Contains(got, c.want) {
			t.Errorf("NextStep = %q, want containing %q", got, c.want)
		}
	}
}
