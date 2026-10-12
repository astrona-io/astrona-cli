package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/labstate"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestUIActivityPausesAndRequiresContinue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	start := now.Add(-10 * time.Minute)
	c := uiActivityClock{target: uiActivityTarget{"astro-lab", start.Format(time.RFC3339), "one"}, lastInput: start, periods: []labstate.ActivityPeriod{{Start: start, End: start}}}
	c.input(start.Add(time.Minute))
	c.update(start.Add(7 * time.Minute))
	if !c.paused || c.total() != time.Minute {
		t.Fatalf("idle counted: %+v", c)
	}
	m := newUIPaneModel(nil, nil, 100, 30)
	defer m.emulator.Close()
	m.title = "SHELL / astro-lab"
	m.resizeContent()
	m.activity = c
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("rm")})
	m.key(tea.KeyMsg{Type: tea.KeyEnter, Paste: true})
	if !m.activity.paused {
		t.Fatal("idle dialog allowed accidental input")
	}
	if !strings.Contains(ansi.Strip(m.View()), "TIME PAUSED") {
		t.Fatal("idle dialog missing")
	}
	// Enter is consumed by the dialog; a nil PTY would expose forwarding.
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.activity.paused || len(m.activity.periods) != 2 {
		t.Fatal("continue did not start a new active period")
	}
	if m.activity.total() != time.Minute {
		t.Fatal("continue counted idle time")
	}
}

func TestUIActivityPersistsAndStopsForReplacement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC().Truncate(time.Second)
	stamp := now.Format(time.RFC3339)
	if err := labstate.Save("astro-lab", &labstate.State{StartedAt: stamp}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "target.json")
	data, _ := json.Marshal(uiActivityTarget{"astro-lab", stamp, "one"})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := newUIPaneModel(nil, nil, 100, 30)
	defer m.emulator.Close()
	m.activityPath = path
	m.activityTick(now)
	m.activity.input(now.Add(time.Minute))
	m.activityTick(now.Add(7 * time.Minute))
	got, tracked, err := labstate.ActiveTime("astro-lab", stamp)
	if err != nil || !tracked || got != time.Minute || !m.activity.paused {
		t.Fatalf("persisted: %v %v %v", got, tracked, err)
	}
	if err := labstate.Save("astro-lab", &labstate.State{StartedAt: now.Add(time.Hour).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	m.activityTick(now.Add(8 * time.Minute))
	if m.activity.target.Lab != "" || m.activity.paused {
		t.Fatal("replacement inherited old idle dialog")
	}
}
