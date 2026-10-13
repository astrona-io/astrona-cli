package labstate

import (
	"testing"
	"time"
)

func TestSubmissionFreezesTimeAcrossWindowsAndRegrades(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	stamp := start.Format(time.RFC3339)
	for window, offset := range map[string]time.Duration{"one": 0, "two": time.Minute, "reopened": 10 * time.Minute} {
		if err := SaveActivity("astro-lab", stamp, window, []ActivityPeriod{{Start: start.Add(offset), End: start.Add(20 * time.Minute)}}); err != nil {
			t.Fatal(err)
		}
		if err := SavePresence("astro-lab", stamp, window, true, start.Add(20*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveSubmission("astro-lab", stamp, false, start.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := SaveSubmission("astro-lab", stamp, true, start.Add(25*time.Minute)); err != nil {
		t.Fatal(err)
	}
	result, err := ReadSubmission("astro-lab", stamp)
	if err != nil || result == nil || result.Pass || !result.At.Equal(start.Add(5*time.Minute)) {
		t.Fatalf("first submission changed: %+v, %v", result, err)
	}
	if total, tracked, err := ActiveTime("astro-lab", stamp); err != nil || !tracked || total != 5*time.Minute {
		t.Fatalf("time continued after submission: %s, %v, %v", total, tracked, err)
	}
	if active, err := HasActiveUI("astro-lab", stamp, start.Add(20*time.Minute)); err != nil || active {
		t.Fatalf("submitted lab stayed active: %v, %v", active, err)
	}
	next := start.Add(time.Hour).Format(time.RFC3339)
	if result, err := ReadSubmission("astro-lab", next); err != nil || result != nil {
		t.Fatalf("new run inherited completion: %+v, %v", result, err)
	}
}

func TestHeadlessSubmissionFreezesElapsedTime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	start := time.Now().UTC().Truncate(time.Second)
	stamp := start.Format(time.RFC3339)
	if err := SaveSubmission("astro-lab", stamp, true, start.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if total, tracked, err := ActiveTime("astro-lab", stamp); err != nil || !tracked || total != 2*time.Minute {
		t.Fatalf("headless time: %s, %v, %v", total, tracked, err)
	}
	if err := SaveActivity("astro-lab", stamp, "reopened", []ActivityPeriod{{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if total, _, err := ActiveTime("astro-lab", stamp); err != nil || total != 2*time.Minute {
		t.Fatalf("reopening changed headless time: %s, %v", total, err)
	}
}
