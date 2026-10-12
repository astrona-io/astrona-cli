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

func TestPresenceAcrossUIsAndCrash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)
	for session, active := range map[string]bool{"working": true, "watching": false} {
		if err := SavePresence("astro-lab", stamp, session, active, now); err != nil {
			t.Fatal(err)
		}
	}
	if active, err := HasActiveUI("astro-lab", stamp, now.Add(time.Second)); err != nil || !active {
		t.Fatalf("watcher overrode worker: %v %v", active, err)
	}
	if active, err := HasActiveUI("astro-lab", stamp, now.Add(16*time.Second)); err != nil || active {
		t.Fatalf("crashed worker stayed active: %v %v", active, err)
	}
	if err := SavePresence("astro-lab", stamp, "working", false, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if active, err := HasActiveUI("astro-lab", stamp, now.Add(3*time.Second)); err != nil || active {
		t.Fatalf("pause not shared: %v %v", active, err)
	}
	if _, tracked, err := ActiveTime("astro-lab", stamp); err != nil || tracked {
		t.Fatal("presence corrupted time records")
	}
}

func TestUsageAveragesDeduplicateUIs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)
	for _, sample := range []struct {
		offset      time.Duration
		cpu, memory float64
	}{{0, 20, 100}, {time.Second, 900, 900}, {5 * time.Second, 40, 200}} {
		if _, err := RecordUsage("astro-lab", stamp, sample.cpu, sample.memory, now.Add(sample.offset)); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := RecordUsage("astro-lab", stamp, 99, 999, now.Add(6*time.Second))
	if err != nil || usage.Samples != 2 || usage.AverageCPU != 30 || usage.AverageMemory != 150 {
		t.Fatalf("bad averages: %+v %v", usage, err)
	}
	if saved, err := ReadUsage("astro-lab", stamp); err != nil || saved.Samples != 2 || saved.CPU != 40 || saved.AverageMemory != 150 {
		t.Fatalf("read measurements: %+v %v", saved, err)
	}
	usage, err = RecordUsage("astro-lab", now.Add(time.Hour).Format(time.RFC3339), 1, 2, now.Add(time.Hour))
	if err != nil || usage.Samples != 1 || usage.AverageCPU != 1 {
		t.Fatal("restart inherited averages")
	}
	if _, _, err := ActiveTime("astro-lab", stamp); err != nil {
		t.Fatal("resources corrupted time records", err)
	}
}

func TestMutationLockRejectsConcurrentReplacement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	release, err := LockMutation("astro-lab")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if unlock, err := LockMutation("astro-lab"); err == nil {
		unlock()
		t.Fatal("concurrent mutation permitted")
	}
	other, err := LockMutation("astro-other")
	if err != nil {
		t.Fatal("independent lab blocked", err)
	}
	other()
	release()
	unlock, err := LockMutation("astro-lab")
	if err != nil {
		t.Fatal("lock not released", err)
	}
	unlock()
}
