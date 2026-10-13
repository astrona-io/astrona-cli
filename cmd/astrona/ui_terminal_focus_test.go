package main

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

func focusInput(t *testing.T, emulator *vt.Emulator, action func(), want string) {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		b := make([]byte, len(want))
		_, _ = io.ReadFull(emulator, b)
		got <- string(b)
	}()
	action()
	select {
	case text := <-got:
		if text != want {
			t.Fatalf("input = %q, want %q", text, want)
		}
	case <-time.After(time.Second):
		t.Fatal("input did not reach the terminal")
	}
}

func TestUITerminalFocusPassesShortcuts(t *testing.T) {
	keys := []struct {
		key  tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyCtrlB}, "\x02"},
		{tea.KeyMsg{Type: tea.KeyCtrlT}, "\x14"},
		{tea.KeyMsg{Type: tea.KeyCtrlN}, "\x0e"},
		{tea.KeyMsg{Type: tea.KeyCtrlP}, "\x10"},
		{tea.KeyMsg{Type: tea.KeyCtrlO}, "\x0f"},
		{tea.KeyMsg{Type: tea.KeyCtrlG}, "\x07"},
		{tea.KeyMsg{Type: tea.KeyCtrlY}, "\x19"},
		{tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}, "\x1d"},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c"), Alt: true}, "\x1bc"},
		{tea.KeyMsg{Type: tea.KeyCtrlPgUp}, "\x1b[5;5~"},
		{tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
	}
	for _, tc := range keys {
		t.Run(tc.key.String(), func(t *testing.T) {
			pane := newUIPaneModel(nil, nil, 120, 30)
			defer pane.emulator.Close()
			pane.title = "TASK / lab"
			pane.terminalLocked = true
			focusInput(t, pane.emulator, func() { pane.Update(tc.key) }, tc.want)
			if pane.controls || pane.selectText || pane.activityAway {
				t.Fatal("outer pane intercepted a shortcut")
			}
			m := &uiTaskModel{terminalLocked: true, tabs: []*uiTerminalTab{{emulator: pane.emulator}}}
			focusInput(t, pane.emulator, func() { m.Update(tc.key) }, tc.want)
			if !m.terminalLocked || m.namingTab || m.taskFocus || m.workspace.action != workspaceNone {
				t.Fatal("shortcut changed Astrona state")
			}
		})
	}
}

func TestUITerminalFocusTransitions(t *testing.T) {
	pane := newUIPaneModel(nil, nil, 120, 30)
	defer pane.emulator.Close()
	m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 30}, tabs: []*uiTerminalTab{{emulator: pane.emulator}}}
	if strings.Contains(ansi.Strip(m.View()), "TERMINALS · LOCKED") {
		t.Fatal("opened already locked")
	}
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}
	focusInput(t, pane.emulator, func() { m.Update(key) }, "a")
	if !m.terminalLocked || !strings.Contains(ansi.Strip(m.View()), "TERMINALS · LOCKED") {
		t.Fatal("typing did not lock focus")
	}
	m.Update(tea.MouseMsg{X: 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.taskFocus || m.selection != nil {
		t.Fatal("mouse left the locked terminal")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket, Alt: true})
	if m.terminalLocked || strings.Contains(ansi.Strip(m.View()), "TERMINALS · LOCKED") {
		t.Fatal("release did not restore app mode")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !m.namingTab {
		t.Fatal("unlocked Astrona shortcuts did not work")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.FocusMsg{})
	if !m.terminalLocked {
		t.Fatal("Continue did not restore terminal focus")
	}
	m.Update(tea.BlurMsg{})
	if m.terminalLocked {
		t.Fatal("pause did not release terminal focus")
	}
	// Pasted escape bytes are terminal data, never Astrona shortcuts.
	focusInput(t, pane.emulator, func() { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\x1b\x1d"), Paste: true}) }, "\x1b\x1d")
	if !m.terminalLocked {
		t.Fatal("paste did not acquire focus")
	}
}

func TestUIIdleReleasesAndRestoresTerminalFocus(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	m := activityTestPane(t, start)
	m.Update(uiPaneOutput{data: []byte("\x1b]777;astrona;ready\a\x1b]777;astrona;locked\a")})
	if !m.taskFocusProtocol || !m.terminalLocked {
		t.Fatal("nested focus state was not received")
	}
	focusInput(t, m.emulator, func() { m.advanceActivity(start.Add(uiIdleAfter)) }, "\x1b[O")
	if m.terminalLocked || !m.activity.paused {
		t.Fatal("idle did not release focus and pause")
	}
	focusInput(t, m.emulator, func() { m.idleKey(tea.KeyMsg{Type: tea.KeyEnter}, start.Add(uiIdleAfter+time.Second)) }, "\x1b[I")
	if !m.terminalLocked || m.activity.paused {
		t.Fatal("Continue did not restore focus and time")
	}
	focusInput(t, m.emulator, func() { m.leaveActivity(start.Add(uiIdleAfter + 2*time.Second)) }, "\x1b[O\x02")
	if m.terminalLocked || !m.activity.paused || !m.activityAway {
		t.Fatal("overview kept focus or resumed time")
	}
}

func TestUITerminalFocusMouseCoordinates(t *testing.T) {
	pane := newUIPaneModel(nil, nil, 120, 30)
	defer pane.emulator.Close()
	_, _ = pane.emulator.WriteString("\x1b[?1000h\x1b[?1006h")
	m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 30}, terminalLocked: true, tabs: []*uiTerminalTab{{emulator: pane.emulator}}}
	left, _, _ := m.size()
	focusInput(t, pane.emulator, func() {
		m.mouse(tea.MouseMsg{X: left + 3, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	}, "\x1b[<0;2;2M")
	focusInput(t, pane.emulator, func() {
		m.mouse(tea.MouseMsg{X: left + 3, Y: 3, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}, "\x1b[<65;2;2M")
}
