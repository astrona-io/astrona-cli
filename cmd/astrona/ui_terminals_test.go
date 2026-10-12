package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

func testUITerminals(t *testing.T) *uiTerminals {
	t.Helper()
	manager := newUITerminals(t.Context(), func(string) (*exec.Cmd, error) {
		cmd := exec.Command("/bin/sh")
		cmd.Env = append(os.Environ(), "PS1=tabs-test> ", "ENV=/dev/null", "TERM=xterm-256color")
		return cmd, nil
	})
	t.Cleanup(manager.close)
	return manager
}

func waitUITab(t *testing.T, tab *uiTerminalTab, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tab.mu.Lock()
		screen := ansi.Strip(uiTerminalScreen(tab.emulator, 0, false))
		tab.mu.Unlock()
		if strings.Contains(screen, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal did not show %q", want)
}

func TestUITerminalTabsKeepSeparateShellsAndResize(t *testing.T) {
	manager := testUITerminals(t)
	for range 2 {
		result := manager.start("", 60, 20)().(uiTerminalStarted)
		if result.err != nil {
			t.Fatal(result.err)
		}
	}
	tabs := manager.snapshot()
	for _, tab := range tabs {
		waitUITab(t, tab, "tabs-test>")
	}
	_, err := io.WriteString(tabs[0].terminal, "export TAB_VALUE=first; printf 'one-%s\\n' ready\n")
	if err != nil {
		t.Fatal(err)
	}
	waitUITab(t, tabs[0], "one-ready")
	_, err = io.WriteString(tabs[1].terminal, "printf 'two-%s\\n' \"${TAB_VALUE:-separate}\"\n")
	if err != nil {
		t.Fatal(err)
	}
	waitUITab(t, tabs[1], "two-separate")
	m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 35, task: "Create a pod"}, terminals: manager, tabs: tabs}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, w, h := m.size()
	for _, tab := range tabs {
		size, err := pty.GetsizeFull(tab.terminal)
		if err != nil || int(size.Cols) != w || int(size.Rows) != h {
			t.Fatalf("resize: %+v %v", size, err)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.active != 1 {
		t.Fatal("next tab did not select second shell")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.active != 0 {
		t.Fatal("previous tab did not select first shell")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.workspace.action != workspaceLeave {
		t.Fatal("Ctrl+C did not return to overview")
	}
	// Returning to Home leaves the shells alive and their output draining.
	_, err = io.WriteString(tabs[0].terminal, "printf 'home-%s\\n' \"$TAB_VALUE\"\n")
	if err != nil {
		t.Fatal(err)
	}
	waitUITab(t, tabs[0], "home-first")
	manager.close()
	for _, tab := range tabs {
		if tab.cmd.ProcessState == nil {
			t.Fatal("shell not reaped on UI close")
		}
	}
}

func TestUITaskLayoutAndFocus(t *testing.T) {
	for _, size := range [][2]int{{120, 35}, {80, 24}, {48, 14}, {20, 8}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := &uiTaskModel{workspace: workspaceModel{width: size[0], height: size[1], task: strings.Repeat("Task line\n", 100)}}
			view := ansi.Strip(m.View())
			if len(strings.Split(view, "\n")) != size[1] {
				t.Fatal("height overflow")
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatal("width overflow")
				}
			}
			if size[0] >= 64 && (!strings.Contains(view, "QUESTION / TASK") || !strings.Contains(view, "TERMINALS")) {
				t.Fatal("missing split panels")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyF2})
			m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
			if !m.taskFocus || m.workspace.scroll == 0 {
				t.Fatal("task cannot be scrolled")
			}
			if size[0] < 64 && !strings.Contains(m.View(), "Task line") {
				t.Fatal("narrow terminal cannot read task")
			}
		})
	}
}

func TestUITaskProcessHelper(t *testing.T) {
	if os.Getenv("ASTRONA_UI_TASK_TEST_HELPER") != "1" {
		return
	}
	ctx, closePool := uiTerminalContext(context.Background())
	defer closePool()
	m := workspaceModel{name: "astro-test-lab", runID: "one", task: "Create a pod beside the terminal", terminalCommand: func(string) (*exec.Cmd, error) {
		cmd := exec.Command("/bin/sh")
		cmd.Env = append(os.Environ(), "PS1=task-shell> ", "ENV=/dev/null")
		return cmd, nil
	}}
	for range 2 {
		result, err := runUITaskScreen(ctx, m, nil)
		if err != nil || result.action != workspaceLeave {
			os.Exit(21)
		}
		uiPaneTitle("HOME")
		resultHome, err := tea.NewProgram(uiHomeModel{notice: "Time paused; terminal tabs retained", data: uiHomeSnapshot{runs: []uiHomeRun{{name: "astro-test-lab", openable: true}}}}, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
		home := resultHome.(uiHomeModel)
		if err != nil {
			os.Exit(22)
		}
		if home.quit {
			return
		}
	}
}

func TestUITaskKeepsTabsAcrossOverview(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUITaskProcessHelper$")
	cmd.Env = append(os.Environ(), "ASTRONA_UI_TASK_TEST_HELPER=1", uiPaneEnv+"=1", "HOME="+t.TempDir(), "TERM=xterm-256color", "CI=", "GITHUB_ACTIONS=")
	input, keys := io.Pipe()
	defer input.Close()
	defer keys.Close()
	out := &uiPaneTestOutput{}
	done := make(chan error, 1)
	go func() {
		code, err := runUIPaneCommand(ctx, cmd, 120, 40, tea.WithInput(input), tea.WithOutput(out))
		if err == nil && code != 0 {
			err = fmt.Errorf("exit %d", code)
		}
		done <- err
	}()
	waitUIPaneOutput(t, out, "task-shell>")
	_, _ = io.WriteString(keys, "export KEEP_TAB=retained; printf 'first-%s\\n' ready\r")
	waitUIPaneOutput(t, out, "first-ready")
	_, _ = io.WriteString(keys, "\x14") // Ctrl+T
	waitUIPaneOutput(t, out, "Terminal 2")
	_, _ = io.WriteString(keys, "printf 'second-%s\\n' \"${KEEP_TAB:-isolated}\"\r")
	waitUIPaneOutput(t, out, "second-isolated")
	_, _ = io.WriteString(keys, "\x10") // Ctrl+P
	_, _ = io.WriteString(keys, "sleep 30\r")
	time.Sleep(100 * time.Millisecond)
	_, _ = io.WriteString(keys, "\x1bc") // Alt+C interrupts the foreground command.
	_, _ = io.WriteString(keys, "printf 'interrupt-%s\\n' works\r")
	waitUIPaneOutput(t, out, "interrupt-works")
	_, _ = io.WriteString(keys, "\x03")
	waitUIPaneOutput(t, out, "Time paused; terminal tabs retained")
	_, _ = io.WriteString(keys, "\r")
	time.Sleep(100 * time.Millisecond)
	_, _ = io.WriteString(keys, "printf 'reopened-%s\\n' \"$KEEP_TAB\"\r")
	waitUIPaneOutput(t, out, "reopened-retained")
	_, _ = io.WriteString(keys, "\x03")
	time.Sleep(100 * time.Millisecond)
	_, _ = io.WriteString(keys, "q")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\x1b[?1049l") != 1 {
		t.Fatal("UI left the alternate screen during navigation")
	}
}
