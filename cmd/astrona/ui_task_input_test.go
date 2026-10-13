package main

import (
	"fmt"
	"io"
	"strings"
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

func TestUITaskMouseFocusAndScrolling(t *testing.T) {
	for _, width := range []int{120, 48} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := &uiTaskModel{workspace: workspaceModel{width: width, height: 24, task: strings.Repeat("Task line\n", 100)}}
			left, _, height := m.size()
			if left == 0 {
				m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
			}
			m.Update(tea.MouseMsg{X: 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			m.Update(tea.MouseMsg{X: 2, Y: 2, Action: tea.MouseActionRelease})
			if !m.taskFocus || m.selection != nil {
				t.Fatal("click did not focus task without selecting text")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyDown})
			if m.workspace.scroll != 1 {
				t.Fatal("focused task did not scroll")
			}
			m.Update(tea.MouseMsg{X: 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
			if m.workspace.scroll != 4 {
				t.Fatal("wheel did not scroll task")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyEnd})
			bottom := len(m.markdownLines) - height
			if m.workspace.scroll != bottom {
				t.Fatal("End did not reach the last page")
			}
			for range 10 {
				m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
			}
			m.Update(tea.KeyMsg{Type: tea.KeyUp})
			if m.workspace.scroll != bottom-1 {
				t.Fatal("overscroll prevented scrolling up from the end")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyHome})
			m.Update(tea.MouseMsg{X: 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
			if m.workspace.scroll != 0 {
				t.Fatal("scrolled above the first line")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
			if m.workspace.scroll != height {
				t.Fatal("Page Down did not scroll one visible page")
			}
			m.Update(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
			if m.workspace.scroll != height {
				t.Fatal("wheel over navigation scrolled task")
			}
			if !strings.Contains(m.View(), "Ctrl+O terminal") || !strings.Contains(m.View(), "Task ") {
				t.Fatal("focus shortcut or scroll position missing")
			}
			if left > 0 {
				m.Update(tea.MouseMsg{X: left + 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
				if m.taskFocus {
					t.Fatal("terminal click did not restore terminal focus")
				}
				m.Update(tea.MouseMsg{X: left + 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
				if m.workspace.scroll != height {
					t.Fatal("wheel over terminal scrolled task")
				}
			} else {
				m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
				if m.taskFocus {
					t.Fatal("Ctrl+O did not restore terminal view")
				}
			}
		})
	}
}

func TestUITaskScrollAfterResizeAndPaste(t *testing.T) {
	m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 24, task: strings.Repeat("A long task line with enough words to wrap in a narrow pane.\n", 100)}, taskFocus: true}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.workspace.width, m.workspace.height = 200, 40
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	_, _, height := m.size()
	if m.workspace.scroll != len(m.markdownLines)-height-1 {
		t.Fatal("resize left scroll past the end")
	}
	before := m.workspace.scroll
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j"), Paste: true})
	if m.workspace.scroll != before {
		t.Fatal("pasted text scrolled the task")
	}
}

func TestUITaskClickReturnsArrowKeysToTerminal(t *testing.T) {
	pane := newUIPaneModel(nil, nil, 120, 30)
	defer pane.emulator.Close()
	m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 30}, taskFocus: true, tabs: []*uiTerminalTab{{emulator: pane.emulator}}}
	left, _, _ := m.size()
	m.Update(tea.MouseMsg{X: left + 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: left + 2, Y: 2, Action: tea.MouseActionRelease})
	read := make(chan string, 1)
	go func() {
		buf := make([]byte, 3)
		_, _ = io.ReadFull(pane.emulator, buf)
		read <- string(buf)
	}()
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	select {
	case got := <-read:
		if got != "\x1b[B" {
			t.Fatalf("terminal received %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("arrow key did not reach the terminal after clicking it")
	}
}
