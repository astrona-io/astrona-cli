package main

import (
	"context"
	"testing"
	"time"

	"astrona/internal/labstate"
)

func TestIsPlaygroundName(t *testing.T) {
	for name, want := range map[string]bool{
		"ATS014/section-010/module-01/playground":            true,
		"astrona-io/ATS014/section-010/module-01/playground": true,
		"ATS014/section-010/module-01/lab-01":                false,
		"playground":                                         false,
	} {
		if isPlaygroundName(name) != want {
			t.Errorf("isPlaygroundName(%q) = %v", name, !want)
		}
	}
}

// The watchdog leaves quietly when there is no playground to watch, or when the
// playground it started for was destroyed or replaced before the deadline.
func TestWatchPlaygroundLeavesWhenThePlaygroundIsGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := watchPlayground(ctx, "astro-none", time.Millisecond); err != nil {
		t.Fatalf("no state: %v", err)
	}

	sess := &labstate.Session{Site: "http://x", ID: "s-1", Kind: "playground", DeadlineAt: time.Now().Add(time.Hour).Format(time.RFC3339)}
	if err := labstate.Save("astro-play", &labstate.State{Session: sess}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- watchPlayground(ctx, "astro-play", 10*time.Millisecond) }()
	time.Sleep(50 * time.Millisecond)
	// A new run replaces the session: the old watchdog has nothing left to do.
	replaced := *sess
	replaced.ID = "s-2"
	if err := labstate.Save("astro-play", &labstate.State{Session: &replaced}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("replaced: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the watchdog kept waiting for a replaced playground")
	}
}

func TestRunningPlaygroundsMatchesByClusterOrCatalogName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	play := &labstate.Session{ID: "s-1", Kind: "playground", Lab: "ATS014/section-010/module-01/playground"}
	lab := &labstate.Session{ID: "s-2", Lab: "ATS014/section-010/module-01/lab-01"}
	if err := labstate.Save("astro-play", &labstate.State{Session: play}); err != nil {
		t.Fatal(err)
	}
	if err := labstate.Save("astro-lab", &labstate.State{Session: lab}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{
		"":           1, // every playground, never a lab
		"astro-play": 1,
		"play":       1,
		"astrona-io/ATS014/section-010/module-01/playground": 1,
		"ATS014/section-010/module-01/playground":            1,
		"astro-lab": 0,
		"ATS014/section-010/module-02/playground": 0,
	} {
		got, err := runningPlaygrounds(name)
		if err != nil || len(got) != want {
			t.Errorf("runningPlaygrounds(%q) = %v, %v; want %d", name, got, err, want)
		}
	}
}
