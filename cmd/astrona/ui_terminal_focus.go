package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// These messages travel only between Astrona's nested UI and its outer pane.
// Terminal applications render through a separate emulator; their OSC output
// never reaches this control channel. No command authorizations travel here.
func uiPaneControl(action string) {
	if os.Getenv(uiPaneEnv) == "1" {
		fmt.Fprintf(os.Stdout, "\x1b]777;astrona;%s\x07", action)
	}
}

func (m *uiPaneModel) taskPane() bool { return strings.HasPrefix(m.title, "TASK / ") }

func (m *uiPaneModel) taskControl(data []byte) bool {
	if !m.taskPane() {
		return false
	}
	switch string(data) {
	case "777;astrona;ready":
		m.taskFocusProtocol, m.terminalLocked = true, false
	case "777;astrona;locked":
		m.terminalLocked = !m.activity.paused || m.activityAway
	case "777;astrona;unlocked":
		m.terminalLocked = false
	case "777;astrona;overview":
		now := time.Now()
		m.activity.update(now)
		m.activity.paused, m.activityAway, m.terminalLocked = true, true, false
		m.saveActivity(now)
	case "777;astrona;copy":
		if !m.activity.paused && !m.activityAway && !m.ended {
			m.selectText, m.mouseDown = true, false
			m.selectionView = ""
			m.taskMouseCmd = tea.DisableMouse
		}
	case "777;astrona;controls":
		if !m.activity.paused && !m.activityAway && !m.ended {
			m.controls, m.scroll = true, 0
		}
	default:
		return false
	}
	return true
}

// Standard focus reports let the outer idle dialog suspend/resume the inner
// model without sending a shortcut to the shell. Physical window focus alone
// does not change this state; the outer UI does not request focus reports.
func (m *uiPaneModel) taskTerminalFocus(locked bool) {
	m.terminalLocked = locked
	if !m.taskPane() || !m.taskFocusProtocol {
		return
	}
	if locked {
		m.emulator.SendText("\x1b[I")
	} else {
		m.emulator.SendText("\x1b[O")
	}
}
