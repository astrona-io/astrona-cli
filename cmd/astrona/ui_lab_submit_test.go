package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

func TestLabSubmitConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
		want workspaceAction
	}{
		{"default no", []tea.KeyMsg{{Type: tea.KeyEnter}}, workspaceNone},
		{"yes", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("y")}}, workspaceSubmit},
		{"select yes", []tea.KeyMsg{{Type: tea.KeyLeft}, {Type: tea.KeyEnter}}, workspaceSubmit},
		{"no", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("n")}}, workspaceNone},
		{"escape", []tea.KeyMsg{{Type: tea.KeyEsc}}, workspaceNone},
		{"interrupt", []tea.KeyMsg{{Type: tea.KeyCtrlC}}, workspaceNone},
		{"overview", []tea.KeyMsg{{Type: tea.KeyCtrlB}}, workspaceLeave},
		{"paste ignored", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("y"), Paste: true}, {Type: tea.KeyEnter}}, workspaceNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &uiTaskModel{workspace: workspaceModel{canSubmit: true, width: 120, height: 30, task: "Keep this task visible"}}
			m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
			if !m.workspace.confirmSubmit || m.workspace.action != workspaceNone {
				t.Fatal("shortcut did not ask before submitting")
			}
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "SUBMIT LAB") || !strings.Contains(view, "[ No ]") || !strings.Contains(view, "QUESTION / TASK") {
				t.Fatal("confirmation missing or replaced the task view")
			}
			for _, key := range tc.keys {
				m.Update(key)
			}
			if m.workspace.action != tc.want || m.workspace.confirmSubmit || m.terminalLocked {
				t.Fatalf("unexpected confirmation result: %+v", m.workspace)
			}
		})
	}
}

func TestLabSubmitGuardsAndTerminalFocus(t *testing.T) {
	emulator := vt.NewEmulator(80, 24)
	defer emulator.Close()
	m := &uiTaskModel{terminalLocked: true, workspace: workspaceModel{canSubmit: true}, tabs: []*uiTerminalTab{{emulator: emulator}}}
	focusInput(t, emulator, func() { m.Update(tea.KeyMsg{Type: tea.KeyCtrlS}) }, "\x13")
	if m.workspace.confirmSubmit {
		t.Fatal("locked editor shortcut opened submission")
	}
	m.setTerminalLocked(false)
	m.workspace.snapshot.blocked = "run changed"
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.workspace.confirmSubmit {
		t.Fatal("blocked run opened confirmation")
	}
	m.workspace.snapshot.blocked = ""
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m.Update(tea.BlurMsg{})
	if m.workspace.confirmSubmit || m.terminalLocked {
		t.Fatal("idle pause left submission or terminal focus active")
	}
	m.Update(tea.FocusMsg{})
	if !m.terminalLocked {
		t.Fatal("Continue did not restore terminal focus after idle cancellation")
	}
	m.setTerminalLocked(false)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m.workspace.snapshot.blocked = "run changed while dialog open"
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.workspace.action != workspaceNone {
		t.Fatal("changed run was submitted")
	}
	m.workspace.canSubmit, m.workspace.snapshot.blocked = false, ""
	focusInput(t, emulator, func() { m.Update(tea.KeyMsg{Type: tea.KeyCtrlS}) }, "\x13")
	if m.workspace.confirmSubmit {
		t.Fatal("playground offered submission")
	}
}

func TestLabSubmitMouse(t *testing.T) {
	for _, width := range []int{48, 80, 120} {
		for _, yes := range []bool{false, true} {
			m := &uiTaskModel{workspace: workspaceModel{canSubmit: true, width: width, height: 30}, tabs: []*uiTerminalTab{{label: "Terminal 1"}, {label: "Terminal 2"}}}
			nav, hits := m.navigation()
			if !strings.Contains(ansi.Strip(nav), "Submit lab") {
				t.Fatalf("missing button at width %d", width)
			}
			for _, hit := range hits {
				if hit.start < m.submitButtonWidth() {
					t.Fatal("tab overlaps submit button")
				}
			}
			m.Update(tea.MouseMsg{X: 3, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			if !m.workspace.confirmSubmit {
				t.Fatal("submit click did not open dialog")
			}
			x, y, _, _, lines := workspaceDialogLayout(m.workspace.submitText(), width, 30)
			for i, line := range lines {
				if strings.Contains(line, "[ No ]") {
					col := 12
					if yes {
						col = 3
					}
					m.Update(tea.MouseMsg{X: x + 2 + col, Y: y + 1 + i, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
					break
				}
			}
			if (m.workspace.action == workspaceSubmit) != yes || m.workspace.confirmSubmit {
				t.Fatalf("mouse confirmation failed at width %d, yes=%v", width, yes)
			}
		}
	}
}

func TestLabSubmitDetailsMouseRouting(t *testing.T) {
	m := newUIPaneModel(nil, nil, 120, 30)
	defer m.emulator.Close()
	m.title = "WORKSPACE / lab"
	_, _ = m.emulator.WriteString("\x1b[?1002h\x1b[?1006h")
	focusInput(t, m.emulator, func() {
		m.Update(tea.MouseMsg{X: 2, Y: uiHeaderHeight(m.width, m.height) + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	}, "\x1b[<0;3;3M")
}
