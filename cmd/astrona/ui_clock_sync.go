package main

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"astrona/internal/labstate"
	"astrona/internal/outbox"
)

// The lab window keeps Astrona's clock in step with its own: when the window
// goes idle (or is left) the run's clock pauses on the site too, and the next
// input resumes it. The idle dialog waits for the answer, so what it says is
// true; offline, the signal is kept on this machine (internal/outbox) and the
// header shows it next to the logo until it is sent.

// uiClockSynced is the outcome of telling Astrona the clock paused or resumed.
type uiClockSynced struct {
	paused bool
	result string // clockSent, clockWaiting, clockSame, clockLocal or clockOthers
	status outbox.Status
	err    error
}

// clockOthers: another lab window of the run is in use, so its clock keeps running.
const clockOthers = "others"

// clockSending: the pause is on its way; the idle dialog waits for it.
const clockSending = "sending"

// queueClock records a change of this window's clock for the next Update to send.
func (m *uiPaneModel) queueClock(target uiActivityTarget, paused bool, at time.Time) {
	if target.Lab == "" {
		return
	}
	m.clockCmds = append(m.clockCmds, m.clockSignal(target, paused, at))
}

func (m *uiPaneModel) clockSignal(target uiActivityTarget, paused bool, at time.Time) tea.Cmd {
	// A local run (no lab session) has no clock on Astrona: nothing to tell.
	if st, err := labstate.Load(target.Lab); err != nil || st == nil || st.LocalOnly || st.Session == nil || st.Session.ID == "" {
		m.clockSync = clockLocal
		return nil
	}
	if paused {
		// Another window still in use keeps the run's clock going.
		if active, err := labstate.HasActiveUI(target.Lab, target.Started, time.Now()); err == nil && active {
			m.clockSync = clockOthers
			return nil
		}
		m.clockSync = clockSending
	}
	return func() tea.Msg {
		result, status, err := signalClock(context.Background(), target.Lab, target.Started, paused, at)
		return uiClockSynced{paused: paused, result: result, status: status, err: err}
	}
}

// syncClock notices this window's pause or resume after any message, and sends it.
func (m *uiPaneModel) syncClock(now time.Time) tea.Cmd {
	if m.activity.target.Lab != "" && m.activity.paused != m.clockPaused {
		m.clockPaused = m.activity.paused
		at := now
		if m.clockPaused && len(m.activity.periods) > 0 {
			// The pause began when the last activity ended (the idle grace removed).
			at = m.activity.periods[len(m.activity.periods)-1].End
		}
		m.queueClock(m.activity.target, m.clockPaused, at)
	}
	cmds := m.clockCmds
	m.clockCmds = nil
	return tea.Batch(cmds...)
}

func (m *uiPaneModel) clockSynced(msg uiClockSynced) {
	if msg.status.State != "" {
		m.syncStatus = msg.status
	}
	if msg.paused == m.clockPaused {
		m.clockSync = msg.result
		if msg.err != nil {
			m.clockSync = clockWaiting
			m.activityError = "Could not save the pause for Astrona: " + msg.err.Error()
		}
	}
}

// clockSyncLine is what the idle dialog says about Astrona's clock.
func (m *uiPaneModel) clockSyncLine() string {
	switch m.clockSync {
	case clockSent, clockSame:
		return "The lab clock on Astrona is paused too."
	case clockWaiting:
		if m.syncStatus.State == outbox.SignedOut {
			return "Signed out: the pause is saved here and sent after astrona login."
		}
		return "No connection to Astrona: the pause is saved here and sent once it is back."
	case clockOthers:
		return "Another lab window is in use, so the lab clock keeps running."
	}
	return ""
}

// refreshSyncStatus rereads the outbox status (every few seconds, from the tick).
func (m *uiPaneModel) refreshSyncStatus(now time.Time) {
	if now.Sub(m.syncChecked) < 5*time.Second {
		return
	}
	m.syncChecked = now
	if status, err := outbox.ReadStatus(); err == nil {
		m.syncStatus = status
	}
}

// syncMarker is the connection state shown with the Astrona logo: empty when
// nothing recent is known (a local run, or no signal sent yet).
func (m *uiPaneModel) syncMarker() (string, lipgloss.Style, bool) {
	s := m.syncStatus
	if s.State == "" || time.Since(s.Checked) > 2*time.Minute && s.Pending == 0 {
		return "", workspaceMuted, false
	}
	waiting := ""
	if s.Pending > 0 {
		waiting = fmt.Sprintf(" · %d WAITING", s.Pending)
	}
	switch s.State {
	case outbox.Offline:
		return "● OFFLINE" + waiting, workspaceDanger, true
	case outbox.SignedOut:
		return "● SIGNED OUT" + waiting, workspaceWarn, true
	}
	if s.Pending > 0 {
		return "● SYNCING" + waiting, workspaceWarn, true
	}
	return "● CONNECTED", lipgloss.NewStyle().Foreground(lipgloss.Color("42")), true
}
