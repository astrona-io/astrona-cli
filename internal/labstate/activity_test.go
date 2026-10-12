package labstate

import (
	"testing"
	"time"
)

func TestActiveTimeUnionsTerminalsAndSeparatesRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	start := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	stamp := start.Format(time.RFC3339)
	if _, tracked, err := ActiveTime("astro-lab", stamp); err != nil || tracked {
		t.Fatalf("untracked lab: %v, %v", tracked, err)
	}
	for name, periods := range map[string][]ActivityPeriod{
		"one": {{start, start.Add(time.Minute)}, {start.Add(10 * time.Minute), start.Add(12 * time.Minute)}},
		"two": {{start.Add(30 * time.Second), start.Add(2 * time.Minute)}},
	} {
		if err := SaveActivity("astro-lab", stamp, name, periods); err != nil {
			t.Fatal(err)
		}
	}
	got, tracked, err := ActiveTime("astro-lab", stamp)
	if err != nil || !tracked || got != 4*time.Minute {
		t.Fatalf("total=%v tracked=%v err=%v", got, tracked, err)
	}
	if _, tracked, err := ActiveTime("astro-lab", start.Add(time.Hour).Format(time.RFC3339)); err != nil || tracked {
		t.Fatal("restart inherited time")
	}
	if err := SaveActivity("../escape", stamp, "one", nil); err == nil {
		t.Fatal("unsafe lab name")
	}
	if err := SaveActivity("astro-lab", stamp, "../escape", nil); err == nil {
		t.Fatal("unsafe session")
	}
}
