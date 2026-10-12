package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m workspaceModel) updateHome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.width < 48 || m.height < 14 {
		switch key {
		case "q", "esc", "ctrl+c", "ctrl+d":
			m.action = workspaceLeave
			return m, tea.Quit
		}
		return m, nil
	}
	switch key {
	case "q", "esc", "ctrl+c", "ctrl+d":
		m.action = workspaceLeave
		return m, tea.Quit
	case "p":
		m.action = workspaceLoadPreferences
		return m, tea.Quit
	case "r":
		m.action = workspaceReloadLabs
		return m, tea.Quit
	case "up", "k":
		m.selected = max(0, m.selected-1)
	case "down", "j":
		m.selected = min(max(0, len(m.labs)-1), m.selected+1)
	case "enter":
		if len(m.labs) > 0 {
			m.action = workspaceSelect
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m workspaceModel) homeView() string {
	w, h := m.width, m.bodyHeight()
	if m.notice != "" {
		h -= 4
	}
	var content string
	if len(m.labs) == 0 {
		content = lipgloss.Place(w-4, h-2, lipgloss.Center, lipgloss.Center,
			workspaceAccent.Render("No labs are running yet.")+"\n\n"+
				"Start a lab with astrona run <lab> --ui.\nPress p to adjust your preferences.")
	} else {
		nameWidth := max(8, w-22)
		rows := []string{workspaceMuted.Render(fmt.Sprintf("  %-*s  %s", nameWidth, "LAB", "STATE"))}
		visible := max(1, h-3)
		start := max(0, m.selected-visible+1)
		for i := start; i < min(len(m.labs), start+visible); i++ {
			name := workspaceFit(sanitizeLine(m.labs[i]), nameWidth, 1)
			row := name + "  running"
			if i == m.selected {
				row = workspaceSelected(row, w-4)
			} else {
				row = "  " + row
			}
			rows = append(rows, row)
		}
		content = strings.Join(rows, "\n")
	}
	body := workspacePanel(fmt.Sprintf("RUNNING LABS [%d]", len(m.labs)), content, w, h, workspaceFocus)
	footer := []string{"p preferences   r refresh   q quit", "↑/↓ select   enter open"}
	if len(m.labs) == 0 {
		footer[1] = "No lab is required for preferences."
	}
	if !m.home {
		footer = []string{"↑/↓ select   enter open   q cancel", "Choose a running lab"}
	}
	if m.notice != "" {
		body += "\n" + workspaceAlert(m.notice, w)
	}
	return m.frame("WORKSPACE", fmt.Sprintf("Kind labs: %d  |  Local workspace", len(m.labs)), body, footer)
}
