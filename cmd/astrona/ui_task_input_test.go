package main

import (
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUITerminalKeysReachTerminal(t *testing.T) {
	for _, tc := range []struct {
		key  tea.KeyType
		want string
	}{{tea.KeyF1, "\x1bOP"}, {tea.KeyF2, "\x1bOQ"}, {tea.KeyF3, "\x1bOR"}, {tea.KeyF4, "\x1bOS"}, {tea.KeyCtrlC, "\x03"}} {
		for _, task := range []bool{false, true} {
			t.Run(tea.KeyMsg{Type: tc.key}.String()+map[bool]string{false: "/pane", true: "/task"}[task], func(t *testing.T) {
				pane := newUIPaneModel(nil, nil, 120, 30)
				pane.title = "TASK / astro-lab"
				defer pane.emulator.Close()
				m := &uiTaskModel{tabs: []*uiTerminalTab{{emulator: pane.emulator}}}
				read := make(chan string, 1)
				go func() {
					buf := make([]byte, len(tc.want))
					_, _ = io.ReadFull(pane.emulator, buf)
					read <- string(buf)
				}()
				key := tea.KeyMsg{Type: tc.key}
				if task {
					m.Update(key)
				} else {
					pane.Update(key)
				}
				select {
				case got := <-read:
					if got != tc.want {
						t.Fatalf("terminal received %q, want %q", got, tc.want)
					}
				case <-time.After(time.Second):
					t.Fatal("terminal key was intercepted")
				}
				if m.taskFocus || m.workspace.action != workspaceNone || pane.selectText || pane.activityAway || pane.activity.paused {
					t.Fatal("terminal key changed UI state")
				}
			})
		}
	}
}

func TestUITaskControlNavigation(t *testing.T) {
	m := &uiTaskModel{}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.taskFocus {
		t.Fatal("task focus did not change")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.taskFocus {
		t.Fatal("terminal focus was not restored")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if cmd == nil || m.workspace.action != workspaceSelect {
		t.Fatal("lab details are inaccessible")
	}
}

func TestUIFunctionKeysDoNotOpenDialogs(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4} {
		msg := tea.KeyMsg{Type: key}
		home := uiHomeModel{data: uiHomeSnapshot{warning: "Example error"}}
		next, _ := home.Update(msg)
		if next.(uiHomeModel).editor.showAlert {
			t.Fatal("function key opened Home alert")
		}
		workspace := workspaceModel{notice: "Example error"}
		next, _ = workspace.Update(msg)
		if next.(workspaceModel).showAlert {
			t.Fatal("function key opened workspace alert")
		}
		run := uiRunModel{}
		next, _ = run.Update(msg)
		if next.(uiRunModel).details {
			t.Fatal("function key opened startup details")
		}
	}
}

func TestUITabNameInterruptCancelsDialog(t *testing.T) {
	m := &uiTaskModel{namingTab: true, tabName: "Monitor"}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || m.namingTab || m.workspace.action != workspaceNone {
		t.Fatal("Ctrl+C left the lab instead of cancelling the tab name")
	}
}
