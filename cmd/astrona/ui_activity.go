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
const uiIdleAfter = 5 * time.Minute

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

func (m *uiPaneModel) activityTick(now time.Time) {
	if m.activityPath == "" || m.ended {
		return
	}
	var target uiActivityTarget
	data, err := os.ReadFile(m.activityPath)
	if err == nil {
		err = json.Unmarshal(data, &target)
	}
	if err != nil && !os.IsNotExist(err) {
		m.activityError = "Could not read activity tracking: " + err.Error()
		return
	}
	if target.Lab != "" {
		st, loadErr := labstate.Load(target.Lab)
		if loadErr != nil {
			m.activityError = "Could not check the active lab: " + loadErr.Error()
			return
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
		m.activityTotal, m.activitySavedOwn = 0, 0
		if target.Lab != "" {
			m.activity.periods = []labstate.ActivityPeriod{{Start: now, End: now}}
		}
	}
	m.usage, _ = labstate.ReadUsage(target.Lab, target.Started)
	if time.Since(m.usage.Updated) > 15*time.Second {
		m.usage = labstate.ResourceUsage{}
	}
	wasPaused := m.activity.paused
	m.activity.update(now)
	if m.activity.paused != wasPaused || now.Sub(m.activity.saved) >= 5*time.Second {
		m.saveActivity(now)
	}
}

func (m *uiPaneModel) saveActivity(now time.Time) {
	if err := m.activity.save(now); err != nil {
		m.activityError = "Could not save time spent: " + err.Error()
	} else {
		m.activityError = ""
		if m.activity.target.Lab != "" {
			total, _, err := labstate.ActiveTime(m.activity.target.Lab, m.activity.target.Started)
			if err != nil {
				m.activityError = "Could not read time spent: " + err.Error()
			} else {
				m.activityTotal, m.activitySavedOwn = total, m.activity.total()
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
	message := "No terminal input for 5 minutes. Your time spent counter is paused.\n\n" +
		"This idle interval is excluded. The lab and running commands keep running.\n" +
		"Exam and playground expiry limits still apply.\n\n[ Continue ]\nEnter resumes · Ctrl+] opens pane controls"
	if m.activityError != "" {
		message += "\n\n" + m.activityError
	}
	return workspaceDialog(background, "IDLE · TIME PAUSED", strings.TrimSpace(message), m.width, m.emulator.Height(), false)
}
