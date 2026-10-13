package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"astrona/internal/labstate"

	tea "github.com/charmbracelet/bubbletea"
)

const uiActivityEnv = "ASTRONA_INTERNAL_UI_ACTIVITY"
const uiIdleAfter = 3 * time.Minute

type uiActivityTarget struct {
	Lab, Started, Session string
}

// Lifecycle code identifies the primary lab through a private control file.
// Terminal titles and shell output cannot select a lab for time tracking.
func uiActivityStart(lab, started string) func() {
	path := os.Getenv(uiActivityEnv)
	if path == "" || started == "" {
		return func() {}
	}
	write := func(target uiActivityTarget) {
		data, _ := json.Marshal(target)
		file, err := os.CreateTemp(filepath.Dir(path), ".activity-")
		if err == nil {
			defer os.Remove(file.Name())
			_, err = file.Write(data)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			if err == nil {
				err = os.Rename(file.Name(), path)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not update activity tracking: %v\n", err)
		}
	}
	write(uiActivityTarget{Lab: lab, Started: started, Session: rand.Text()})
	return func() { write(uiActivityTarget{}) }
}

type uiActivityClock struct {
	target           uiActivityTarget
	periods          []labstate.ActivityPeriod
	lastInput, saved time.Time
	paused           bool
}

type uiActivityTick time.Time

func uiActivityTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg { return uiActivityTick(now) })
}

func (c *uiActivityClock) update(now time.Time) {
	if c.target.Lab == "" || c.paused || len(c.periods) == 0 {
		return
	}
	end := now
	if !now.Before(c.lastInput.Add(uiIdleAfter)) {
		// Once inactivity is confirmed, remove the idle grace interval too.
		end, c.paused = c.lastInput, true
	}
	p := &c.periods[len(c.periods)-1]
	if end.Before(p.Start) {
		end = p.Start
	}
	p.End = end
}

func (c *uiActivityClock) input(now time.Time) {
	if c.paused {
		c.periods = append(c.periods, labstate.ActivityPeriod{Start: now, End: now})
		c.paused = false
	}
	c.lastInput = now
	c.update(now)
}

func (c *uiActivityClock) save(now time.Time) error {
	if c.target.Lab == "" {
		return nil
	}
	c.update(now)
	if err := labstate.SaveActivity(c.target.Lab, c.target.Started, c.target.Session, c.periods); err != nil {
		return err
	}
	if err := labstate.SavePresence(c.target.Lab, c.target.Started, c.target.Session, !c.paused, now); err != nil {
		return err
	}
	c.saved = now
	return nil
}

// Check inactivity before file reads or input handling, so neither a delayed
// refresh nor a failed read can postpone the idle dialog.
func (m *uiPaneModel) advanceActivity(now time.Time) tea.Cmd {
	wasPaused := m.activity.paused
	m.activity.update(now)
	if wasPaused || !m.activity.paused || m.activityAway {
		return nil
	}
	m.idleOverview = false
	m.controls, m.closing, m.mouseDown = false, false, false
	restoreMouse := m.selectText
	m.selectText, m.selectionView = false, ""
	m.saveActivity(now)
	if restoreMouse {
		return tea.EnableMouseCellMotion
	}
	return nil
}

func (m *uiPaneModel) activityTick(now time.Time) tea.Cmd {
	if m.ended {
		return nil
	}
	restoreMouse := m.advanceActivity(now)
	if m.activityPath == "" {
		return restoreMouse
	}
	var target uiActivityTarget
	data, err := os.ReadFile(m.activityPath)
	if err == nil {
		err = json.Unmarshal(data, &target)
	}
	if err != nil && !os.IsNotExist(err) {
		m.activityError = "Could not read activity tracking: " + err.Error()
		return restoreMouse
	}
	if target.Lab != "" {
		st, loadErr := labstate.Load(target.Lab)
		if loadErr != nil {
			m.activityError = "Could not check the active lab: " + loadErr.Error()
			return restoreMouse
		}
		if st == nil || st.StartedAt != target.Started {
			target = uiActivityTarget{}
		}
	}
	if target != m.activity.target {
		m.activity.update(now)
		m.activity.paused = true
		m.saveActivity(now)
		if m.activityError != "" {
			m.failure = m.activityError
		}
		m.activity = uiActivityClock{target: target, lastInput: now}
		m.activityAway = false
		m.activityTimeFrozen, m.idleOverview = false, false
		m.activityTotal, m.activitySavedOwn = 0, 0
		if target.Lab != "" {
			m.activity.periods = []labstate.ActivityPeriod{{Start: now, End: now}}
		}
	}
	m.usage, _ = labstate.ReadUsage(target.Lab, target.Started)
	if time.Since(m.usage.Updated) > 15*time.Second {
		m.usage = labstate.ResourceUsage{}
	}
	if now.Sub(m.activity.saved) >= 5*time.Second {
		m.saveActivity(now)
	}
	return restoreMouse
}

func (m *uiPaneModel) saveActivity(now time.Time) {
	if err := m.activity.save(now); err != nil {
		m.activityError = "Could not save time spent: " + err.Error()
	} else {
		m.activityError = ""
		if m.activity.target.Lab != "" {
			// Other windows may keep working in the same lab. Their time belongs
			// in the overview, but must not advance this window's paused counter.
			if m.activity.paused && m.activityTimeFrozen {
				return
			}
			total, _, err := labstate.ActiveTime(m.activity.target.Lab, m.activity.target.Started)
			if err != nil {
				m.activityError = "Could not read time spent: " + err.Error()
			} else {
				m.activityTotal, m.activitySavedOwn = total, m.activity.total()
				m.activityTimeFrozen = m.activity.paused
			}
		}
	}
}

func (c *uiActivityClock) total() time.Duration {
	var total time.Duration
	for _, period := range c.periods {
		total += max(time.Duration(0), period.End.Sub(period.Start))
	}
	return total
}

func (m *uiPaneModel) timeSpent() time.Duration {
	return max(time.Duration(0), m.activityTotal+m.activity.total()-m.activitySavedOwn)
}

func (m *uiPaneModel) idleView(background string) string {
	choices := "[ Continue ]    Overview"
	if m.idleOverview {
		choices = "Continue    [ Overview ]"
	}
	message := "No input in this lab window for 3 minutes.\n" +
		"Time spent paused at " + formatUptime(m.timeSpent()) + ".\n\n" +
		"This idle interval is excluded. The lab and running commands keep running.\n" +
		"Exam and playground expiry limits still apply.\n\n" + choices +
		"\n←/→ or Tab selects · Enter confirms · Esc goes to overview"
	if m.activityError != "" {
		message += "\n\n" + m.activityError
	}
	return workspaceDialog(background, "IDLE · TIME PAUSED", strings.TrimSpace(message), m.width, m.emulator.Height(), false)
}

func (m *uiPaneModel) idleKey(key tea.KeyMsg, now time.Time) tea.Cmd {
	if key.Paste {
		return nil
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab":
		m.idleOverview = !m.idleOverview
	case "enter":
		if !m.idleOverview {
			m.activity.input(now)
			m.saveActivity(now)
			return nil
		}
		m.leaveActivity(now)
	case "esc", "ctrl+c":
		m.leaveActivity(now)
	}
	return nil
}

func (m *uiPaneModel) leaveActivity(now time.Time) {
	m.activity.paused, m.activityAway = true, true
	m.saveActivity(now)
	key := tea.KeyMsg{Type: tea.KeyCtrlC}
	if m.title == "PREFERENCES" {
		key = tea.KeyMsg{Type: tea.KeyCtrlO, Alt: true}
	}
	sendUIPaneKey(m.emulator, key)
}
