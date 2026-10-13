package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const uiSubmitButton = " [Submit lab] "

func (m *workspaceModel) askSubmit() {
	if m.canSubmit && m.snapshot.blocked == "" {
		m.confirmSubmit, m.submitYes, m.exitMenu = true, false, false
	}
}

func (m workspaceModel) submitKey(key tea.KeyMsg) (workspaceModel, tea.Cmd) {
	if key.Paste {
		return m, nil
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab":
		m.submitYes = !m.submitYes
	case "y":
		m.submitYes = true
		return m.finishSubmit()
	case "enter":
		return m.finishSubmit()
	case "ctrl+b":
		m.confirmSubmit, m.submitYes = false, false
		m.action = workspaceLeave
		return m, tea.Quit
	case "n", "esc", "ctrl+c":
		m.confirmSubmit, m.submitYes = false, false
	}
	return m, nil
}

func (m workspaceModel) finishSubmit() (workspaceModel, tea.Cmd) {
	confirmed := m.submitYes && m.canSubmit && m.snapshot.blocked == ""
	m.confirmSubmit, m.submitYes = false, false
	if confirmed {
		m.action = workspaceSubmit
		return m, tea.Quit
	}
	return m, nil
}

func (m workspaceModel) submitText() string {
	choices := "  Yes      [ No ]"
	if m.submitYes {
		choices = "[ Yes ]      No"
	}
	return "Ready to submit this lab?\n\nRun the lab's checks and save the result.\nAccount results follow this run's tracking settings.\nThe lab and your terminals stay running.\n\n" + choices +
		"\n\n←/→ or Tab selects · Enter confirms\ny yes · n / Esc cancel"
}

func (m workspaceModel) submitView(background string) string {
	return workspaceDialog(background, "SUBMIT LAB", m.submitText(), m.width, m.height, false)
}

func (m workspaceModel) submitMouse(msg tea.MouseMsg) (workspaceModel, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	x, y, _, h, lines := workspaceDialogLayout(m.submitText(), m.width, m.height)
	for i, line := range lines {
		if i >= h-2 || !strings.Contains(line, "Yes") || !strings.Contains(line, "No") || msg.Y != y+1+i {
			continue
		}
		col := msg.X - x - 2
		if col >= 0 && col < 7 {
			m.submitYes = true
			return m.finishSubmit()
		}
		if col >= 9 && col < 17 {
			m.submitYes = false
			return m.finishSubmit()
		}
	}
	return m, nil
}
