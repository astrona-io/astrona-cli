package exam

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestStateTiming(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	s := State{StartedAt: start, Limit: 2 * time.Hour}

	at := start.Add(72 * time.Minute)
	if s.Over(at) || s.Remaining(at) != 48*time.Minute {
		t.Fatalf("remaining = %s over=%v", s.Remaining(at), s.Over(at))
	}
	if got := s.Summary(at); got != "1h12m0s of 2h0m0s used (48m0s left)" {
		t.Errorf("Summary = %q", got)
	}
	late := start.Add(2*time.Hour + 5*time.Minute)
	if !s.Over(late) || !strings.HasPrefix(s.Summary(late), "over time by 5m0s") {
		t.Errorf("late Summary = %q", s.Summary(late))
	}
	if Round(42*time.Second+300*time.Millisecond) != "42s" {
		t.Errorf("Round = %q", Round(42*time.Second))
	}
}

func TestStartLoadClear(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if s, err := Load("astro-x"); s != nil || err != nil {
		t.Fatalf("no exam = %v, %v", s, err)
	}
	now := time.Now().Truncate(time.Second)
	if err := Start("astro-x", 90*time.Minute, now); err != nil {
		t.Fatal(err)
	}
	s, err := Load("astro-x")
	if err != nil || s == nil || !s.StartedAt.Equal(now) || s.Limit != 90*time.Minute {
		t.Fatalf("Load = %+v, %v", s, err)
	}
	path, _ := statePath("astro-x")
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}

	// Start again (reset) restarts the clock.
	later := now.Add(time.Hour)
	Start("astro-x", 90*time.Minute, later)
	if s, _ := Load("astro-x"); !s.StartedAt.Equal(later) {
		t.Error("restart didn't reset the clock")
	}

	if err := Clear("astro-x"); err != nil {
		t.Fatal(err)
	}
	if s, _ := Load("astro-x"); s != nil {
		t.Error("state survived Clear")
	}
	if err := Clear("astro-x"); err != nil {
		t.Errorf("second Clear: %v", err)
	}
	if _, err := statePath("../x"); err == nil {
		t.Error("unsafe lab name accepted")
	}
}
