package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/spf13/cobra"
)

func TestWantsUIPane(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"run", "lab", "--ui"}, true},
		{[]string{"run", "lab", "--ui=true"}, true},
		{[]string{"--verbose", "run", "--ui", "lab"}, true},
		{[]string{"run", "--ui-startup", "--", "lab"}, true},
		{[]string{"ui"}, true},
		{[]string{"ui", "astro-lab"}, true},
		{[]string{"ui", "--help"}, false},
		{[]string{"run", "lab", "--ui", "-h"}, false},
		{[]string{"help", "run"}, false},
		{[]string{"run", "lab", "--ui=false"}, false},
		{[]string{"run", "--", "--ui"}, false},
		{[]string{"run", "--git", "--ui"}, false},
		{[]string{"run", "lab"}, false},
		{[]string{"shell", "lab"}, false},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if got := wantsUIPane(tc.args); got != tc.want {
				t.Fatalf("wantsUIPane = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUIPaneKeyEncoding(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want string
		key              tea.KeyMsg
	}{
		{"unicode", "", "界", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("界")}},
		{"interrupt", "", "\x03", tea.KeyMsg{Type: tea.KeyCtrlC}},
		{"eof", "", "\x04", tea.KeyMsg{Type: tea.KeyCtrlD}},
		{"enter", "", "\r", tea.KeyMsg{Type: tea.KeyEnter}},
		{"backspace", "", "\x7f", tea.KeyMsg{Type: tea.KeyBackspace}},
		{"alt", "", "\x1bx", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}},
		{"history", "", "\x1b[A", tea.KeyMsg{Type: tea.KeyUp}},
		{"application cursor", "\x1b[?1h", "\x1bOA", tea.KeyMsg{Type: tea.KeyUp}},
		{"details", "", "\x1bOP", tea.KeyMsg{Type: tea.KeyF1}},
		{"shift tab", "", "\x1b[Z", tea.KeyMsg{Type: tea.KeyShiftTab}},
		{"paste", "\x1b[?2004h", "\x1b[200~first\nsecond\x1b[201~", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first\nsecond"), Paste: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emulator := vt.NewEmulator(80, 24)
			_, _ = emulator.WriteString(tc.mode)
			input := make(chan string, 1)
			go func() {
				buf := make([]byte, len(tc.want))
				_, _ = io.ReadFull(emulator, buf)
				input <- string(buf)
			}()
			sendUIPaneKey(emulator, tc.key)
			if got := <-input; got != tc.want {
				t.Fatalf("input = %q, want %q", got, tc.want)
			}
			_ = emulator.Close()
		})
	}
}

func TestUIPaneScreenAndControls(t *testing.T) {
	for _, size := range [][2]int{{20, 8}, {80, 24}, {140, 45}} {
		m := newUIPaneModel(nil, nil, size[0], size[1])
		_, _ = m.emulator.WriteString("\x1b]2;SHELL / lab\a\x1b[2J\x1b[H界 hello\r\n")
		for i := range 100 {
			_, _ = fmt.Fprintf(m.emulator, "output %d\r\n", i)
		}
		m.key(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
		m.key(tea.KeyMsg{Type: tea.KeyHome})
		if !m.controls || !strings.Contains(ansi.Strip(m.screen()), "界 hello") {
			t.Fatal("cannot scroll back to earlier shell output")
		}
		view := ansi.Strip(m.View())
		if len(strings.Split(view, "\n")) != size[1] {
			t.Fatal("pane exceeds terminal height")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("pane exceeds terminal width")
			}
		}
		m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		if !m.closing || m.key(tea.KeyMsg{Type: tea.KeyEnter}) != nil || m.closing {
			t.Fatal("closing did not require explicit confirmation")
		}
		_ = m.emulator.Close()
	}
}

func TestUIPaneResizesChildTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	m := newUIPaneModel(master, nil, 80, 24)
	m.title = "WORKSPACE / lab"
	defer m.emulator.Close()
	for _, size := range [][2]int{{120, 40}, {52, 18}, {80, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		got, err := pty.GetsizeFull(slave)
		if err != nil || int(got.Cols) != size[0] || int(got.Rows) != size[1]-uiHeaderHeight(size[0], size[1])-1 {
			t.Fatalf("child terminal size = %+v, %v", got, err)
		}
		if m.emulator.Width() != int(got.Cols) || m.emulator.Height() != int(got.Rows) {
			t.Fatal("emulator and child dimensions differ")
		}
	}
}

func TestUIPaneFailureKeepsFinalOutputVisible(t *testing.T) {
	m := newUIPaneModel(nil, nil, 80, 24)
	defer m.emulator.Close()
	_, _ = fmt.Fprintf(m.emulator, "\x1b[%d;1HLast error detail", m.emulator.Height())
	m.failure, m.ended = "Command failed", true
	view := ansi.Strip(m.View())
	for _, want := range []string{"Last error detail", "Command failed", "Enter / Esc close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("failure screen hides %q", want)
		}
	}
}

type uiPaneTestOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *uiPaneTestOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *uiPaneTestOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func waitUIPaneOutput(t *testing.T, out *uiPaneTestOutput, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(ansi.Strip(out.String()), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missing %q in terminal output:\n%s", want, ansi.Strip(out.String()))
}

// The helper runs in a real PTY, including the production confirmation and
// shell attachment code, without creating a lab or accessing the network.
func TestUIPaneProcessHelper(t *testing.T) {
	if os.Getenv("ASTRONA_UI_PANE_TEST_HELPER") != "1" {
		return
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	yes, err := confirmUIRun(cmd, uiRunNotice{title: "START LAB", summary: "Start the terminal test lab?"})
	if err != nil || !yes {
		os.Exit(3)
	}
	fmt.Println("Repository download stays in the pane")
	fmt.Fprintln(os.Stderr, "Bootstrap output stays in the pane")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		os.Exit(18)
	}
	workspaceKubeconfigFixture(t, "astro-test-lab")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "pane-shell> ")
	t.Setenv("ENV", "/dev/null")
	m := workspaceModel{name: "astro-test-lab", task: "Pane test task", snapshot: workspaceSnapshot{clusters: []workspaceCluster{{name: "astro-test-lab", status: "Ready"}}}}
	m, err = runWorkspaceScreen(context.Background(), m, nil)
	if err != nil || m.action != workspaceShell {
		os.Exit(19)
	}
	if err := runLabShell("astro-test-lab", "", nil); err != nil {
		os.Exit(19)
	}
	m.task = "Workspace restored after shell exit"
	m, err = runWorkspaceScreen(context.Background(), m, nil)
	if err != nil || m.action != workspaceLeave {
		os.Exit(19)
	}
	fmt.Fprintln(os.Stderr, "Error: synthetic setup failure")
	os.Exit(17)
}

func TestUIPaneKeepsSetupShellAndErrorsVisible(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUIPaneProcessHelper$")
	cmd.Env = append(os.Environ(), "ASTRONA_UI_PANE_TEST_HELPER=1", uiPaneEnv+"=1", "HOME="+t.TempDir(), "TERM=xterm-256color", "CI=", "GITHUB_ACTIONS=")
	input, keys := io.Pipe()
	defer input.Close()
	defer keys.Close()
	out := &uiPaneTestOutput{}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := runUIPaneCommand(ctx, cmd, 100, 30, tea.WithInput(input), tea.WithOutput(out))
		done <- result{code, err}
	}()
	waitUIPaneOutput(t, out, "Start the terminal test lab?")
	_, _ = io.WriteString(keys, "y")
	waitUIPaneOutput(t, out, "Repository download stays in the pane")
	waitUIPaneOutput(t, out, "Bootstrap output stays in the pane")
	_, _ = io.WriteString(keys, "\r")
	waitUIPaneOutput(t, out, "Pane test task")
	_, _ = io.WriteString(keys, "s")
	waitUIPaneOutput(t, out, "pane-shell>")
	_, _ = io.WriteString(keys, "printf 'shell-%s\\n' works\r")
	waitUIPaneOutput(t, out, "shell-works")
	_, _ = io.WriteString(keys, "sleep 30\r")
	time.Sleep(100 * time.Millisecond)
	_, _ = io.WriteString(keys, "\x03")
	_, _ = io.WriteString(keys, "printf 'interrupt-%s\\n' survived\r")
	waitUIPaneOutput(t, out, "interrupt-survived")
	_, _ = io.WriteString(keys, "exit\r")
	waitUIPaneOutput(t, out, "Workspace restored after shell exit")
	_, _ = io.WriteString(keys, "q")
	waitUIPaneOutput(t, out, "LEAVE WORKSPACE")
	_, _ = io.WriteString(keys, "k")
	waitUIPaneOutput(t, out, "synthetic setup failure")
	waitUIPaneOutput(t, out, "Command ended (exit 17)")
	if strings.Contains(out.String(), "\x1b[?1049l") {
		t.Fatal("physical terminal left the UI before the user closed it")
	}
	select {
	case <-done:
		t.Fatal("error closed the UI without acknowledgement")
	default:
	}
	_, _ = io.WriteString(keys, "\r")
	got := <-done
	if got.err != nil || got.code != 17 {
		t.Fatalf("exit = %d, %v", got.code, got.err)
	}
}

func TestUIPaneCloseStopsAndReapsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "printf 'Waiting in pane\\n'; read answer")
	input, keys := io.Pipe()
	defer input.Close()
	defer keys.Close()
	out := &uiPaneTestOutput{}
	done := make(chan error, 1)
	go func() {
		code, err := runUIPaneCommand(ctx, cmd, 80, 24, tea.WithInput(input), tea.WithOutput(out))
		if err == nil && code != 130 {
			err = fmt.Errorf("closed pane exit = %d", code)
		}
		done <- err
	}()
	waitUIPaneOutput(t, out, "Waiting in pane")
	_, _ = io.WriteString(keys, "\x1d")
	waitUIPaneOutput(t, out, "Esc resume")
	_, _ = io.WriteString(keys, "q")
	waitUIPaneOutput(t, out, "Close the UI and stop its active command?")
	_, _ = io.WriteString(keys, "y")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("pane left its child process unreaped")
	}
}
