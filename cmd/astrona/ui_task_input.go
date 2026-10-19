package main

import (
	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

func (m *uiTaskModel) mouse(msg tea.MouseMsg) tea.Cmd {
	if m.workspace.confirmSubmit {
		var cmd tea.Cmd
		m.workspace, cmd = m.workspace.submitMouse(msg)
		return cmd
	}
	if m.terminalLocked {
		left, width, height := m.size()
		inTerminal := msg.X >= left+2 && msg.X < left+2+width && msg.Y >= 2 && msg.Y < height+2
		inWorkspace := msg.X >= 0 && msg.X < m.workspace.width && msg.Y >= 0 && msg.Y < m.workspace.height
		if !inTerminal && inWorkspace && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			m.setTerminalLocked(false)
		} else {
			return m.terminalMouse(msg)
		}
	}
	if m.namingTab {
		return nil
	}
	left, _, height := m.size()
	inPanel := msg.X >= 0 && msg.X < m.workspace.width && msg.Y >= 1 && msg.Y < height+3
	overTask := inPanel && (left > 0 && msg.X < left || left == 0 && m.taskFocus)
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if overTask && !m.dragging {
			m.selection = nil
			delta := 3
			if msg.Button == tea.MouseButtonWheelUp {
				delta = -delta
			}
			m.scrollTask(delta)
		}
		return nil
	}
	if msg.Action == tea.MouseActionRelease {
		m.dragging = false
		if m.selection != nil && m.selection.dragging {
			m.selection.move(msg.X, msg.Y)
			m.selection.dragging = false
			if m.selection.moved {
				return copyUISelection(m.selection)
			}
			m.selection = nil
		}
		return nil
	}
	if msg.Action == tea.MouseActionPress {
		m.selection = nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && left > 0 && msg.Y >= 1 && msg.Y <= height+2 && msg.X >= left-1 && msg.X <= left {
		m.dragging = true
	}
	if m.dragging && msg.Button == tea.MouseButtonLeft {
		m.taskWidth = min(60, max(20, (msg.X+1)*100/max(1, m.workspace.width)))
		m.terminals.taskWidth = m.taskWidth
		m.resize()
		return nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y == 0 {
		if m.workspace.canSubmit && msg.X >= 0 && msg.X < m.submitButtonWidth() {
			m.workspace.askSubmit()
			return nil
		}
		_, hits := m.navigation()
		for _, hit := range hits {
			if msg.X >= hit.start && msg.X < hit.end {
				m.active, m.taskFocus = hit.index, false
				break
			}
		}
	}
	if msg.Button == tea.MouseButtonLeft {
		switch msg.Action {
		case tea.MouseActionPress:
			if inPanel {
				m.taskFocus = overTask
			}
			m.beginSelection(msg.X, msg.Y)
		case tea.MouseActionMotion:
			if m.selection != nil && m.selection.dragging {
				m.selection.move(msg.X, msg.Y)
			}
		}
	}
	return nil
}

func (m *uiTaskModel) key(msg tea.KeyMsg) tea.Cmd {
	if m.workspace.confirmSubmit {
		var cmd tea.Cmd
		m.workspace, cmd = m.workspace.submitKey(msg)
		if m.workspace.action == workspaceLeave {
			uiPaneControl("overview")
		}
		return cmd
	}
	if !msg.Paste && (msg.Type == tea.KeyF2 && !msg.Alt || msg.Type == tea.KeyCtrlCloseBracket && msg.Alt) {
		m.setTerminalLocked(false)
		return nil
	}
	if m.terminalLocked {
		m.sendTerminalKey(msg)
		return nil
	}
	if m.selection != nil {
		m.selection = nil
		if !msg.Paste && msg.Type == tea.KeyEsc {
			return nil
		}
	}
	if m.namingTab && (msg.Paste || msg.String() != "ctrl+b") {
		return m.nameTab(msg)
	}
	if !msg.Paste {
		switch msg.String() {
		case "ctrl+s":
			if m.workspace.canSubmit {
				m.workspace.askSubmit()
				return nil
			}
		case "enter":
			if len(m.tabs) == 0 {
				return m.start()
			}
		case "ctrl+y":
			uiPaneControl("copy")
			return nil
		case "ctrl+]":
			uiPaneControl("controls")
			return nil
		case "ctrl+b":
			uiPaneControl("overview")
			m.workspace.action = workspaceLeave
			return tea.Quit
		case "ctrl+t":
			if !m.starting {
				m.namingTab, m.tabName = true, ""
			}
			return nil
		case "ctrl+n", "ctrl+p":
			if len(m.tabs) > 0 {
				step := 1
				if msg.String() == "ctrl+p" {
					step = len(m.tabs) - 1
				}
				m.active = (m.active + step) % len(m.tabs)
			}
			return nil
		case "ctrl+o":
			m.taskFocus = !m.taskFocus
			return nil
		case "ctrl+g":
			m.workspace.action = workspaceSelect
			return tea.Quit
		}
	}
	if m.taskFocus {
		if msg.Paste {
			return nil
		}
		_, _, height := m.size()
		switch msg.String() {
		case "down", "j":
			m.scrollTask(1)
		case "up", "k":
			m.scrollTask(-1)
		case "pgdown":
			m.scrollTask(height)
		case "pgup":
			m.scrollTask(-height)
		case "home":
			m.workspace.scroll = 0
		case "end":
			m.scrollTask(0)
			m.workspace.scroll = max(0, len(m.markdownLines)-height)
		}
		return nil
	}
	if len(m.tabs) > 0 {
		tab := m.tabs[m.active]
		tab.mu.Lock()
		if tab.disposed {
			tab.mu.Unlock()
			return nil
		}
		if !msg.Paste {
			switch msg.String() {
			case "shift+pgup":
				tab.scroll = min(tab.emulator.ScrollbackLen(), tab.scroll+tab.emulator.Height())
				tab.mu.Unlock()
				return nil
			case "shift+pgdown":
				tab.scroll = max(0, tab.scroll-tab.emulator.Height())
				tab.mu.Unlock()
				return nil
			}
		}
		tab.mu.Unlock()
	}
	m.sendTerminalKey(msg)
	return nil
}

func (m *uiTaskModel) sendTerminalKey(msg tea.KeyMsg) {
	if m.active < 0 || m.active >= len(m.tabs) {
		return
	}
	tab := m.tabs[m.active]
	tab.mu.Lock()
	defer tab.mu.Unlock()
	if tab.disposed || tab.ended {
		return
	}
	m.setTerminalLocked(true)
	tab.scroll = 0
	sendUIPaneKey(tab.emulator, msg)
}

func (m *uiTaskModel) setTerminalLocked(locked bool) {
	if m.terminalLocked == locked {
		return
	}
	m.terminalLocked = locked
	m.selection, m.dragging = nil, false
	if locked {
		m.taskFocus, m.namingTab = false, false
		uiPaneControl("locked")
	} else {
		uiPaneControl("unlocked")
	}
}

func (m *uiTaskModel) terminalMouse(msg tea.MouseMsg) tea.Cmd {
	if m.active < 0 || m.active >= len(m.tabs) {
		return nil
	}
	left, width, height := m.size()
	x, y := msg.X-left-2, msg.Y-2
	if x < 0 || x >= width || y < 0 || y >= height {
		return nil
	}
	tab := m.tabs[m.active]
	tab.mu.Lock()
	defer tab.mu.Unlock()
	if tab.disposed || tab.ended {
		return nil
	}
	mouse := uv.Mouse{X: x, Y: y, Button: uv.MouseLeft}
	switch msg.Button {
	case tea.MouseButtonRight:
		mouse.Button = uv.MouseRight
	case tea.MouseButtonMiddle:
		mouse.Button = uv.MouseMiddle
	case tea.MouseButtonWheelUp:
		mouse.Button = uv.MouseWheelUp
	case tea.MouseButtonWheelDown:
		mouse.Button = uv.MouseWheelDown
	}
	if msg.Alt {
		mouse.Mod |= uv.ModAlt
	}
	if msg.Ctrl {
		mouse.Mod |= uv.ModCtrl
	}
	if msg.Shift {
		mouse.Mod |= uv.ModShift
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown:
		tab.emulator.SendMouse(uv.MouseWheelEvent(mouse))
	case msg.Action == tea.MouseActionPress:
		tab.emulator.SendMouse(uv.MouseClickEvent(mouse))
	case msg.Action == tea.MouseActionMotion:
		tab.emulator.SendMouse(uv.MouseMotionEvent(mouse))
	case msg.Action == tea.MouseActionRelease:
		tab.emulator.SendMouse(uv.MouseReleaseEvent(mouse))
	}
	return nil
}

func (m *uiTaskModel) scrollTask(delta int) {
	left, width, height := m.size()
	if left > 0 {
		width = left - 4
	}
	m.taskPage(width, height)
	m.workspace.scroll = min(max(0, m.workspace.scroll+delta), max(0, len(m.markdownLines)-height))
}
