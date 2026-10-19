package main

import (
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

func TestUITerminalReleaseKeys(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyF2}, {Code: ']', Mod: tea.ModCtrl | tea.ModAlt}} {
		t.Run(key.String(), func(t *testing.T) {
			m := &uiTaskModel{terminalLocked: true}
			m.Update(key)
			if m.terminalLocked {
				t.Fatal("could not release focus without forwarding input")
			}
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			if m.workspace.action != workspaceLeave {
				t.Fatal("release did not restore navigation")
			}
		})
	}
	if runtime.GOOS == "darwin" && !strings.Contains(uiReleaseFocusKey(), "Fn+F2") {
		t.Fatal("missing Mac function-key hint")
	}
}

func TestUITerminalMouseReleasesFocus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		x, y   int
		button tea.MouseButton
		locked bool
	}{
		{"task pane", 2, 3, tea.MouseLeft, false},
		{"tab bar", 50, 0, tea.MouseLeft, false},
		{"terminal body", 60, 3, tea.MouseLeft, true},
		{"task wheel", 2, 3, tea.MouseWheelDown, true},
		{"outside workspace", -1, -1, tea.MouseLeft, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &uiTaskModel{terminalLocked: true, workspace: workspaceModel{width: 120, height: 30}}
			if tc.button == tea.MouseWheelDown {
				m.Update(tea.MouseWheelMsg{X: tc.x, Y: tc.y, Button: tc.button})
			} else {
				m.Update(tea.MouseClickMsg{X: tc.x, Y: tc.y, Button: tc.button})
			}
			if m.terminalLocked != tc.locked {
				t.Fatalf("locked=%v, want %v", m.terminalLocked, tc.locked)
			}
		})
	}
}

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
		key  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, "\x02"},
		{tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}, "\x14"},
		{tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}, "\x0e"},
		{tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, "\x10"},
		{tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}, "\x0f"},
		{tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}, "\x07"},
		{tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}, "\x19"},
		{tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl}, "\x1d"},
		{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{tea.KeyPressMsg{Text: "c", Mod: tea.ModAlt}, "\x1bc"},
		{tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl}, "\x1b[5;5~"},
		{tea.KeyPressMsg{Code: tea.KeyEscape}, "\x1b"},
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
	if strings.Contains(ansi.Strip(m.render()), "TERMINALS · LOCKED") {
		t.Fatal("opened already locked")
	}
	key := tea.KeyPressMsg{Text: "a"}
	focusInput(t, pane.emulator, func() { m.Update(key) }, "a")
	if !m.terminalLocked || !strings.Contains(ansi.Strip(m.render()), "TERMINALS · LOCKED") {
		t.Fatal("typing did not lock focus")
	}
	left, _, _ := m.size()
	m.Update(tea.MouseClickMsg{X: left + 3, Y: 3, Button: tea.MouseLeft})
	if m.taskFocus || m.selection != nil {
		t.Fatal("mouse left the locked terminal")
	}
	m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl | tea.ModAlt})
	if m.terminalLocked || strings.Contains(ansi.Strip(m.render()), "TERMINALS · LOCKED") {
		t.Fatal("release did not restore app mode")
	}
	m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if !m.namingTab {
		t.Fatal("unlocked Astrona shortcuts did not work")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(tea.FocusMsg{})
	if !m.terminalLocked {
		t.Fatal("Continue did not restore terminal focus")
	}
	m.Update(tea.BlurMsg{})
	if m.terminalLocked {
		t.Fatal("pause did not release terminal focus")
	}
	// Pasted escape bytes are terminal data, never Astrona shortcuts.
	focusInput(t, pane.emulator, func() { m.Update(tea.PasteMsg{Content: "\x1b\x1d"}) }, "\x1b\x1d")
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
	focusInput(t, m.emulator, func() { m.idleKey(uiKey{key: tea.KeyPressMsg{Code: tea.KeyEnter}}, start.Add(uiIdleAfter+time.Second)) }, "\x1b[I")
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
		m.mouse(tea.MouseClickMsg{X: left + 3, Y: 3, Button: tea.MouseLeft})
	}, "\x1b[<0;2;2M")
	focusInput(t, pane.emulator, func() {
		m.mouse(tea.MouseWheelMsg{X: left + 3, Y: 3, Button: tea.MouseWheelDown})
	}, "\x1b[<65;2;2M")
}
