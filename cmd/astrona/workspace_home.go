package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Use the normal command entrypoint so startup keeps its settings, terms,
// trust prompts and version handover. It inherits the UI's terminal pane.
func runWorkspaceChild(ctx context.Context, command, lab string) error {
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find Astrona executable: %w", err)
	}
	args := []string{command}
	if command == "run" {
		args = append(args, "--ui-startup")
	}
	args = append(args, "--", lab)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == uiRunDeclinedExitCode {
			return errUIRunDeclined
		}
		return fmt.Errorf("could not open lab %s; retry with astrona %s %s: %w", lab, command, lab, err)
	}
	return nil
}

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
	if m.enteringLab {
		switch key {
		case "esc", "ctrl+c", "ctrl+d":
			m.enteringLab = false
		case "enter":
			m.labDraft = strings.TrimSpace(m.labDraft)
			if m.labDraft == "" {
				m.notice = "Enter a catalog lab ID or local path."
				return m, nil
			}
			m.enteringLab, m.action = false, workspaceStart
			return m, tea.Quit
		case "backspace", "ctrl+h":
			runes := []rune(m.labDraft)
			if len(runes) > 0 {
				m.labDraft = string(runes[:len(runes)-1])
			}
		case "ctrl+u":
			m.labDraft = ""
		default:
			if msg.Type == tea.KeyRunes {
				for _, r := range msg.Runes {
					if unicode.IsPrint(r) && len(m.labDraft) < 1024 {
						m.labDraft += string(r)
					}
				}
			}
		}
		return m, nil
	}
	switch key {
	case "q", "esc", "ctrl+c", "ctrl+d":
		m.action = workspaceLeave
		return m, tea.Quit
	case "n":
		m.enteringLab, m.notice = true, ""
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
		if len(m.labs) == 0 {
			m.enteringLab, m.notice = true, ""
		} else {
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
				"Press n to start a lab.\nPress p to adjust your preferences.")
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
	footer := []string{"n start lab   p preferences   r refresh   q quit", "↑/↓ select   enter open"}
	if len(m.labs) == 0 {
		footer[1] = "Enter starts a lab. No lab is required for preferences."
	}
	if !m.home {
		footer = []string{"↑/↓ select   enter open   q cancel", "Choose a running Kind lab"}
	}
	if m.enteringLab {
		input := ansi.TruncateLeft(sanitizeLine(m.labDraft), min(76, w-6)-11, "…")
		body = workspaceDialog(body, "START LAB", "Catalog lab ID or local path\nExample: ./labs/my-lab\n\nLab: "+input+"_", w, h, false)
		footer = []string{"enter start   Ctrl+u clear   Esc cancel", "Review startup, then follow setup inside the UI."}
	}
	if m.notice != "" {
		body += "\n" + workspaceAlert(m.notice, w)
	}
	return m.frame("WORKSPACE", fmt.Sprintf("Kind labs: %d  |  Local workspace", len(m.labs)), body, footer)
}
