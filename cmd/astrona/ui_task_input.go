package main

import tea "github.com/charmbracelet/bubbletea"

func (m *uiTaskModel) mouse(msg tea.MouseMsg) {
	if m.namingTab {
		return
	}
	left, _, height := m.size()
	if msg.Action == tea.MouseActionRelease {
		m.dragging = false
		return
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && left > 0 && msg.Y >= 1 && msg.Y <= height+2 && msg.X >= left-1 && msg.X <= left {
		m.dragging = true
	}
	if m.dragging && msg.Button == tea.MouseButtonLeft {
		m.taskWidth = min(60, max(20, (msg.X+1)*100/max(1, m.workspace.width)))
		m.terminals.taskWidth = m.taskWidth
		m.resize()
		return
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y == 0 {
		_, hits := m.navigation()
		for _, hit := range hits {
			if msg.X >= hit.start && msg.X < hit.end {
				m.active, m.taskFocus = hit.index, false
				break
			}
		}
	}
}

func (m *uiTaskModel) key(msg tea.KeyMsg) tea.Cmd {
	if m.namingTab && (msg.Paste || msg.String() != "ctrl+c") {
		return m.nameTab(msg)
	}
	if !msg.Paste {
		switch msg.String() {
		case "enter":
			if len(m.tabs) == 0 {
				return m.start()
			}
		case "ctrl+c":
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
		switch msg.String() {
		case "down", "j":
			m.workspace.scroll++
		case "up", "k":
			m.workspace.scroll = max(0, m.workspace.scroll-1)
		case "pgdown":
			m.workspace.scroll += max(1, m.workspace.height/2)
		case "pgup":
			m.workspace.scroll = max(0, m.workspace.scroll-max(1, m.workspace.height/2))
		}
		return nil
	}
	if len(m.tabs) > 0 {
		tab := m.tabs[m.active]
		tab.mu.Lock()
		defer tab.mu.Unlock()
		if tab.disposed {
			return nil
		}
		if !msg.Paste {
			switch msg.String() {
			case "shift+pgup":
				tab.scroll = min(tab.emulator.ScrollbackLen(), tab.scroll+tab.emulator.Height())
				return nil
			case "shift+pgdown":
				tab.scroll = max(0, tab.scroll-tab.emulator.Height())
				return nil
			}
		}
		tab.scroll = 0
		if msg.String() == "alt+c" && !msg.Paste {
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		}
		if !tab.ended {
			sendUIPaneKey(tab.emulator, msg)
		}
	}
	return nil
}
