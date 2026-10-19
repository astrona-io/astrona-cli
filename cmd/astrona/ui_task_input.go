package main

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

func (m *uiTaskModel) mouse(msg tea.MouseMsg) tea.Cmd {
	event := msg.Mouse()
	_, click := msg.(tea.MouseClickMsg)
	_, release := msg.(tea.MouseReleaseMsg)
	_, motion := msg.(tea.MouseMotionMsg)
	if m.workspace.confirmSubmit {
		var cmd tea.Cmd
		m.workspace, cmd = m.workspace.submitMouse(msg)
		return cmd
	}
	if m.terminalLocked {
		left, width, height := m.size()
		inTerminal := event.X >= left+2 && event.X < left+2+width && event.Y >= 2 && event.Y < height+2
		inWorkspace := event.X >= 0 && event.X < m.workspace.width && event.Y >= 0 && event.Y < m.workspace.height
		if !inTerminal && inWorkspace && event.Button == tea.MouseLeft && click {
			m.setTerminalLocked(false)
		} else {
			return m.terminalMouse(msg)
		}
	}
	if m.namingTab {
		return nil
	}
	left, _, height := m.size()
	inPanel := event.X >= 0 && event.X < m.workspace.width && event.Y >= 1 && event.Y < height+3
	overTask := inPanel && (left > 0 && event.X < left || left == 0 && m.taskFocus)
	if _, wheel := msg.(tea.MouseWheelMsg); wheel {
		if (event.Button == tea.MouseWheelUp || event.Button == tea.MouseWheelDown) && overTask && !m.dragging {
			m.selection = nil
			delta := 3
			if event.Button == tea.MouseWheelUp {
				delta = -delta
			}
			m.scrollTask(delta)
		}
		return nil
	}
	if release {
		m.dragging = false
		if m.selection != nil && m.selection.dragging {
			m.selection.move(event.X, event.Y)
			m.selection.dragging = false
			if m.selection.moved {
				return copyUISelection(m.selection)
			}
			m.selection = nil
		}
		return nil
	}
	if click {
		m.selection = nil
	}
	if click && event.Button == tea.MouseLeft && left > 0 && event.Y >= 1 && event.Y <= height+2 && event.X >= left-1 && event.X <= left {
		m.dragging = true
	}
	if m.dragging && event.Button == tea.MouseLeft {
		m.taskWidth = min(60, max(20, (event.X+1)*100/max(1, m.workspace.width)))
		m.terminals.taskWidth = m.taskWidth
		m.resize()
		return nil
	}
	if click && event.Button == tea.MouseLeft && event.Y == 0 {
		if m.workspace.canSubmit && event.X >= 0 && event.X < m.submitButtonWidth() {
			m.workspace.askSubmit()
			return nil
		}
		_, hits := m.navigation()
		for _, hit := range hits {
			if event.X >= hit.start && event.X < hit.end {
				m.active, m.taskFocus = hit.index, false
				break
			}
		}
	}
	if event.Button == tea.MouseLeft {
		switch {
		case click:
			if inPanel {
				m.taskFocus = overTask
			}
			m.beginSelection(event.X, event.Y)
		case motion:
			if m.selection != nil && m.selection.dragging {
				m.selection.move(event.X, event.Y)
			}
		}
	}
	return nil
}

func (m *uiTaskModel) key(msg uiKey) tea.Cmd {
	if m.workspace.confirmSubmit {
		var cmd tea.Cmd
		if msg.paste {
			return nil
		}
		m.workspace, cmd = m.workspace.submitKey(msg.key)
		if m.workspace.action == workspaceLeave {
			uiPaneControl("overview")
		}
		return cmd
	}
	if msg.is("f2") || msg.is("ctrl+alt+]") {
		m.setTerminalLocked(false)
		return nil
	}
	if m.terminalLocked {
		m.sendTerminalKey(msg)
		return nil
	}
	if m.selection != nil {
		m.selection = nil
		if msg.is("esc") {
			return nil
		}
	}
	if m.namingTab && (msg.paste || msg.String() != "ctrl+b") {
		return m.nameTab(msg)
	}
	if !msg.paste {
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
		if msg.paste {
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
		if !msg.paste {
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

func (m *uiTaskModel) sendTerminalKey(msg uiKey) {
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
	event := msg.Mouse()
	x, y := event.X-left-2, event.Y-2
	if x < 0 || x >= width || y < 0 || y >= height {
		return nil
	}
	tab := m.tabs[m.active]
	tab.mu.Lock()
	defer tab.mu.Unlock()
	if tab.disposed || tab.ended {
		return nil
	}
	mouse := uv.Mouse{X: x, Y: y, Button: uv.MouseLeft, Mod: event.Mod}
	switch event.Button {
	case tea.MouseRight, tea.MouseMiddle, tea.MouseWheelUp, tea.MouseWheelDown:
		mouse.Button = event.Button
	}
	switch msg.(type) {
	case tea.MouseWheelMsg:
		if event.Button == tea.MouseWheelUp || event.Button == tea.MouseWheelDown {
			tab.emulator.SendMouse(uv.MouseWheelEvent(mouse))
		}
	case tea.MouseClickMsg:
		tab.emulator.SendMouse(uv.MouseClickEvent(mouse))
	case tea.MouseMotionMsg:
		tab.emulator.SendMouse(uv.MouseMotionEvent(mouse))
	case tea.MouseReleaseMsg:
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
