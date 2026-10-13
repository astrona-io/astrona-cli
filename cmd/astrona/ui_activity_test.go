package main

import (
	"encoding/json"
	"io"
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

func activityTestPane(t *testing.T, now time.Time) *uiPaneModel {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	stamp := now.UTC().Format(time.RFC3339)
	if err := labstate.Save("astro-lab", &labstate.State{StartedAt: stamp}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "target.json")
	data, _ := json.Marshal(uiActivityTarget{"astro-lab", stamp, "one"})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := newUIPaneModel(nil, nil, 120, 30)
	t.Cleanup(func() { _ = m.emulator.Close() })
	m.title, m.activityPath = "TASK / astro-lab", path
	m.activityTick(now)
	return m
}

func TestUIIdleDeadlineIgnoresOutputAndRefresh(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	m := activityTestPane(t, start)
	// One minute of work, followed by eleven minutes with output but no input.
	m.activity.input(start.Add(time.Minute))
	for seconds := 61; seconds <= 12*60; seconds++ {
		now := start.Add(time.Duration(seconds) * time.Second)
		m.Update(uiPaneOutput{data: []byte("\rbackground output")})
		m.Update(uiLoadingTick{})
		m.Update(uiAccountStatus{label: "Logged in", signedIn: true})
		m.Update(uiHomeSnapshot{})
		m.Update(tea.FocusMsg{})
		m.Update(tea.BlurMsg{})
		m.Update(uiActivityTick(now))
		if m.activity.lastInput != start.Add(time.Minute) {
			t.Fatal("output, focus or refresh counted as input")
		}
		if seconds < 4*60 {
			if m.activity.paused {
				t.Fatal("paused before three minutes of inactivity")
			}
			continue
		}
		if !m.activity.paused || m.timeSpent() != time.Minute {
			t.Fatalf("at %ds: paused=%v time=%s", seconds, m.activity.paused, m.timeSpent())
		}
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"No input in this lab window for 3 minutes", "[ Continue ]", "Overview", "Time spent paused at " + formatUptime(time.Minute), "Time spent: " + formatUptime(time.Minute) + " · Paused"} {
		if !strings.Contains(view, want) {
			t.Fatalf("idle view missing %q", want)
		}
	}
	if active, err := labstate.HasActiveUI("astro-lab", m.activity.target.Started, start.Add(12*time.Minute)); err != nil || active {
		t.Fatalf("overview still sees this window as active: %v, %v", active, err)
	}
}

func TestUIPausedCounterIgnoresOtherWindows(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	m := activityTestPane(t, start)
	m.activity.input(start.Add(time.Minute))
	m.activityTick(start.Add(4 * time.Minute))
	if !m.activity.paused || m.timeSpent() != time.Minute {
		t.Fatal("window did not pause")
	}
	for minute := 5; minute <= 15; minute++ {
		now := start.Add(time.Duration(minute) * time.Minute)
		other := []labstate.ActivityPeriod{{Start: start, End: now}}
		if err := labstate.SaveActivity("astro-lab", m.activity.target.Started, "other-window", other); err != nil {
			t.Fatal(err)
		}
		if err := labstate.SavePresence("astro-lab", m.activity.target.Started, "other-window", true, now); err != nil {
			t.Fatal(err)
		}
		m.activityTick(now)
		if !m.activity.paused || m.timeSpent() != time.Minute {
			t.Fatal("another window advanced the paused counter")
		}
	}
	total, _, err := labstate.ActiveTime("astro-lab", m.activity.target.Started)
	if err != nil || total != 15*time.Minute {
		t.Fatalf("overview total = %s, %v", total, err)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}, start.Add(16*time.Minute))
	if m.activity.paused || m.activityTimeFrozen || m.activity.total() != time.Minute || m.timeSpent() != 15*time.Minute {
		t.Fatal("Continue did not resume and synchronize the shared total")
	}
}

func TestUIIdleOverridesCopyModeAndReadErrors(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	m := activityTestPane(t, start)
	m.selectText, m.selectionView, m.controls = true, "frozen copy view", true
	if err := os.WriteFile(m.activityPath, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := m.activityTick(start.Add(3 * time.Minute))
	if cmd == nil || !m.activity.paused || m.controls || m.selectText || m.selectionView != "" {
		t.Fatal("read error or copy mode concealed the idle dialog")
	}
	if !strings.Contains(ansi.Strip(m.View()), "IDLE · TIME PAUSED") {
		t.Fatal("idle dialog missing")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlY}, {Type: tea.KeyCtrlCloseBracket}, {Type: tea.KeyRunes, Runes: []rune("typed elsewhere")}, {Type: tea.KeyEnter, Paste: true}} {
		m.handleKey(key, start.Add(4*time.Minute))
		if !m.activity.paused || m.selectText || m.controls {
			t.Fatal("input bypassed the idle dialog")
		}
	}
}

func TestUIInputAfterDeadlineCannotSkipPause(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		t.Run(map[bool]string{false: "keyboard", true: "mouse"}[mouse], func(t *testing.T) {
			start := time.Now().Add(-4 * time.Minute).UTC().Truncate(time.Second)
			m := activityTestPane(t, start)
			m.idleOverview = true
			if mouse {
				m.Update(tea.MouseMsg{X: 2, Y: uiHeaderHeight(m.width, m.height) + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			} else {
				m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("do not send")})
			}
			if !m.activity.paused || m.idleOverview || m.activity.lastInput != start || m.timeSpent() != 0 {
				t.Fatal("first input after a delayed tick bypassed the idle dialog")
			}
		})
	}
}

func TestUIIdleOverviewChoice(t *testing.T) {
	for _, title := range []string{"TASK / astro-lab", "WORKSPACE / astro-lab", "PREFERENCES"} {
		t.Run(title, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			m := activityTestPane(t, start)
			m.title = title
			m.activityTick(start.Add(3 * time.Minute))
			m.handleKey(tea.KeyMsg{Type: tea.KeyTab}, start.Add(4*time.Minute))
			if !m.idleOverview || !strings.Contains(ansi.Strip(m.View()), "[ Overview ]") {
				t.Fatal("Overview could not be selected")
			}
			want := "\x02"
			if title == "PREFERENCES" {
				want = "\x1b\x0f"
			}
			read := make(chan string, 1)
			go func() {
				buf := make([]byte, len(want))
				_, _ = io.ReadFull(m.emulator, buf)
				read <- string(buf)
			}()
			m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}, start.Add(4*time.Minute))
			select {
			case got := <-read:
				if got != want {
					t.Fatalf("overview input = %q", got)
				}
			case <-time.After(time.Second):
				t.Fatal("overview action was not forwarded")
			}
			if !m.activityAway || !m.activity.paused || strings.Contains(ansi.Strip(m.View()), "IDLE · TIME PAUSED") {
				t.Fatal("Overview resumed activity or kept the dialog open")
			}
		})
	}
	m := workspaceModel{showPreferences: true}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO, Alt: true})
	if cmd == nil || next.(workspaceModel).action != workspaceLeave {
		t.Fatal("idle Overview did not leave preferences")
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

func TestUIOverviewStopsActivityImmediately(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	m := newUIPaneModel(nil, nil, 100, 30)
	defer m.emulator.Close()
	m.title = "TASK / astro-lab"
	m.activity = uiActivityClock{target: uiActivityTarget{"astro-lab", now.Format(time.RFC3339), "one"}, lastInput: now, periods: []labstate.ActivityPeriod{{Start: now.Add(-time.Minute), End: now}}}
	input := make(chan byte, 1)
	go func() { buf := make([]byte, 1); _, _ = m.emulator.Read(buf); input <- buf[0] }()
	m.key(tea.KeyMsg{Type: tea.KeyCtrlB})
	if got := <-input; got != 2 {
		t.Fatalf("key forwarded: %d", got)
	}
	// The inner task model decides whether this is a shell key or navigation.
	m.Update(uiPaneOutput{data: []byte("\x1b]777;astrona;overview\a")})
	if !m.activity.paused || !m.activityAway {
		t.Fatal("overview navigation did not pause immediately")
	}
	before := m.activity.total()
	m.activity.update(now.Add(time.Hour))
	if m.activity.total() != before {
		t.Fatal("overview counted as activity")
	}
	if strings.Contains(ansi.Strip(m.View()), "IDLE · TIME PAUSED") {
		t.Fatal("overview obstructed by idle dialog")
	}
}

func TestUIActivityResumesAfterOverview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC().Truncate(time.Second)
	stamp := now.Format(time.RFC3339)
	if err := labstate.Save("astro-lab", &labstate.State{StartedAt: stamp}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "target.json")
	t.Setenv(uiActivityEnv, path)
	m := newUIPaneModel(nil, nil, 100, 30)
	defer m.emulator.Close()
	m.activityPath = path
	stop := uiActivityStart("astro-lab", stamp)
	m.activityTick(now)
	m.activity.paused, m.activityAway = true, true
	m.saveActivity(now.Add(time.Second))
	stop()
	// Reopening may replace the control file before the next tick sees Home.
	defer uiActivityStart("astro-lab", stamp)()
	m.activityTick(now.Add(2 * time.Second))
	if m.activity.paused || m.activityAway {
		t.Fatal("reopened lab stayed paused")
	}
	if active, err := labstate.HasActiveUI("astro-lab", stamp, now.Add(3*time.Second)); err != nil || !active {
		t.Fatalf("resume not published: %v %v", active, err)
	}
}
