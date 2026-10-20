package main

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

func TestUITextSelection(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		start, end       image.Point
	}{
		{"forward", "hello world", "hello", image.Pt(0, 0), image.Pt(4, 0)},
		{"backward", "hello world", "hello", image.Pt(4, 0), image.Pt(0, 0)},
		{"multiline", "first line\nsecond line", "line\nsecond", image.Pt(6, 0), image.Pt(5, 1)},
		{"wide character", "a界b", "界b", image.Pt(2, 0), image.Pt(3, 0)},
		{"combining character", "ae\u0301b", "e\u0301b", image.Pt(1, 0), image.Pt(2, 0)},
		{"styled", "\x1b[31mred\x1b[0m plain", "red", image.Pt(0, 0), image.Pt(2, 0)},
		{"clamped", "first\nlast", "first\nlast", image.Pt(0, 0), image.Pt(100, 100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newUITextSelection(tc.text, image.Rect(2, 3, 18, 5), tc.start.X+2, tc.start.Y+3, false)
			s.move(tc.end.X+2, tc.end.Y+3)
			if got := s.text(); got != tc.want {
				t.Fatalf("selected = %q, want %q", got, tc.want)
			}
			view := s.render()
			if !strings.Contains(view, "48;2;120;220;255") {
				t.Fatalf("selection has no visible highlight: %q", view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > 16 {
					t.Fatal("highlight changed the pane width")
				}
			}
			if s.buffer.Render() == view {
				t.Fatal("selection changed the source buffer")
			}
		})
	}
}

func TestUITaskMouseSelection(t *testing.T) {
	for _, task := range []bool{false, true} {
		t.Run(map[bool]string{false: "terminal", true: "task"}[task], func(t *testing.T) {
			emulator := vt.NewEmulator(80, 20)
			defer emulator.Close()
			_, _ = emulator.WriteString("hello terminal")
			m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 25, task: "hello task"}, tabs: []*uiTerminalTab{{emulator: emulator}}}
			left, _, _ := m.size()
			x, y := left+2, 2
			if task {
				x = 2
				// Isolate hit testing from the Markdown renderer's margins.
				m.markdownSource, m.markdownWidth = m.workspace.task, left-4
				m.markdownLines = []string{"hello task"}
			}
			m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseMotionMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
			_, cmd := m.Update(tea.MouseReleaseMsg{X: x + 4, Y: y})
			if cmd == nil || m.selection == nil || m.selection.task != task || m.selection.text() != "hello" || m.selection.dragging {
				t.Fatal("drag did not select and request copying the pane's text")
			}
			if !strings.Contains(m.render(), "48;2;120;220;255") {
				t.Fatal("task view lost the selection highlight")
			}
			_, _ = emulator.WriteString("\r\x1b[2Knew output")
			if !task && strings.Contains(ansi.Strip(m.render()), "new output") {
				t.Fatal("live output replaced the selected text")
			}
			selected := m.selection
			m.Update(uiClipboardResult{selection: selected})
			if !strings.Contains(m.render(), "Copied to clipboard") {
				t.Fatal("copy result missing")
			}
			m.Update(uiClipboardResult{selection: selected, err: errors.New("clipboard unavailable")})
			if !strings.Contains(m.render(), "Copy failed") {
				t.Fatal("failed copy was reported as success")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.selection != nil || (!task && !strings.Contains(ansi.Strip(m.render()), "new output")) {
				t.Fatal("Esc did not restore live output")
			}
			m.Update(uiClipboardResult{selection: selected})
			if strings.Contains(m.render(), "Copied to clipboard") {
				t.Fatal("stale copy result replaced the footer")
			}
		})
	}
}

func TestUILockedTerminalShiftDragCopiesWithoutUnlocking(t *testing.T) {
	emulator := vt.NewEmulator(80, 20)
	defer emulator.Close()
	_, _ = emulator.WriteString("hello terminal")
	m := &uiTaskModel{terminalLocked: true, workspace: workspaceModel{width: 120, height: 25}, tabs: []*uiTerminalTab{{emulator: emulator}}}
	left, _, _ := m.size()
	m.Update(tea.MouseClickMsg{X: left + 2, Y: 2, Button: tea.MouseLeft, Mod: tea.ModShift})
	m.Update(tea.MouseMotionMsg{X: left + 6, Y: 2, Button: tea.MouseLeft, Mod: tea.ModShift})
	_, copyCmd := m.Update(tea.MouseReleaseMsg{X: left + 6, Y: 2})
	if copyCmd == nil || !m.terminalLocked || m.selection == nil || m.selection.text() != "hello" || m.selection.dragging {
		t.Fatal("locked selection did not request copying without unlocking")
	}
	m.Update(uiClipboardResult{selection: m.selection})
	if !strings.Contains(m.render(), "Copied to clipboard") {
		t.Fatal("locked terminal hid copy confirmation")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.selection != nil || !m.terminalLocked {
		t.Fatal("clearing selection changed terminal focus")
	}
}

func TestUITextSelectionStaysInsidePane(t *testing.T) {
	s := newUITextSelection("task only", image.Rect(2, 2, 14, 3), 2, 2, true)
	s.move(80, 20)
	if s.text() != "task only" {
		t.Fatalf("selection crossed the pane boundary: %q", s.text())
	}
	m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 25, task: "task"}}
	m.Update(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: 2, Y: 2})
	if cmd != nil || m.selection != nil {
		t.Fatal("a click copied text")
	}
	m.selection = s
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	if m.selection != nil {
		t.Fatal("resize kept a selection at stale coordinates")
	}
}

func TestUIClipboardCommand(t *testing.T) {
	for _, tc := range []struct {
		name, platform, available string
		env                       map[string]string
		want                      []string
	}{
		{"macOS", "darwin", "pbcopy", nil, []string{"/tools/pbcopy"}},
		{"Wayland", "linux", "wl-copy", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, []string{"/tools/wl-copy"}},
		{"X11", "linux", "xclip", map[string]string{"DISPLAY": ":0"}, []string{"/tools/xclip", "-selection", "clipboard"}},
		{"X11 fallback", "linux", "xsel", map[string]string{"DISPLAY": ":0"}, []string{"/tools/xsel", "--clipboard", "--input"}},
		{"headless", "linux", "xclip", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := uiClipboardCommand(t.Context(), tc.platform, func(key string) string { return tc.env[key] }, func(name string) (string, error) {
				if name == tc.available {
					return "/tools/" + name, nil
				}
				return "", os.ErrNotExist
			})
			if tc.want == nil {
				if err == nil {
					t.Fatal("no desktop clipboard was reported as available")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(cmd.Args, tc.want) {
				t.Fatalf("clipboard command = %v, %v", cmd, err)
			}
		})
	}
}

func TestUISelectionCopiesPlainText(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix clipboard fixture")
	}
	dir := t.TempDir()
	name := "pbcopy"
	if runtime.GOOS == "linux" {
		name = "wl-copy"
	}
	path := filepath.Join(dir, "clipboard")
	t.Setenv("PATH", dir)
	t.Setenv("WAYLAND_DISPLAY", "test")
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("COPY_TARGET", path)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n/bin/cat > \"$COPY_TARGET\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := newUITextSelection("\x1b[31mhello 界\x1b[0m", image.Rect(0, 0, 20, 1), 0, 0, false)
	s.move(7, 0)
	msg := copyUISelection(s)().(uiClipboardResult)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hello 界" {
		t.Fatalf("clipboard = %q, %v", got, err)
	}
}
