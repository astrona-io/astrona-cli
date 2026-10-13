package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

// Keep the selected pane still while output continues to drain into its terminal.
type uiTextSelection struct {
	area        image.Rectangle
	anchor, end image.Point
	buffer      uv.ScreenBuffer
	task        bool
	dragging    bool
	moved       bool
	status      string
}

func newUITextSelection(content string, area image.Rectangle, x, y int, task bool) *uiTextSelection {
	buffer := uv.NewScreenBuffer(area.Dx(), area.Dy())
	uv.NewStyledString(content).Draw(buffer, buffer.Bounds())
	s := &uiTextSelection{area: area, buffer: buffer, task: task, dragging: true}
	s.anchor = s.point(x, y)
	s.end = s.anchor
	return s
}

func (s *uiTextSelection) point(x, y int) image.Point {
	return image.Pt(min(max(0, x-s.area.Min.X), s.area.Dx()-1), min(max(0, y-s.area.Min.Y), s.area.Dy()-1))
}

func (s *uiTextSelection) move(x, y int) {
	s.end = s.point(x, y)
	s.moved = s.moved || s.end != s.anchor
}

func (s *uiTextSelection) contains(x, y, width int) bool {
	start, end := s.anchor.Y*s.area.Dx()+s.anchor.X, s.end.Y*s.area.Dx()+s.end.X
	if start > end {
		start, end = end, start
	}
	cell := y*s.area.Dx() + x
	return s.moved && cell <= end && cell+width > start
}

func (s *uiTextSelection) render() string {
	buffer := uv.NewRenderBuffer(s.area.Dx(), s.area.Dy())
	for y := range s.area.Dy() {
		for x := 0; x < s.area.Dx(); {
			cell := uv.EmptyCell
			if saved := s.buffer.CellAt(x, y); saved != nil {
				cell = *saved
			}
			width := max(1, cell.Width)
			if s.contains(x, y, width) {
				cell.Style = uv.Style{Fg: color.RGBA{R: 16, G: 24, B: 32, A: 255}, Bg: color.RGBA{R: 120, G: 220, B: 255, A: 255}}
			}
			cell.Link = uv.Link{}
			buffer.SetCell(x, y, &cell)
			x += width
		}
	}
	return buffer.Render()
}

func (s *uiTextSelection) text() string {
	var lines []string
	for y := min(s.anchor.Y, s.end.Y); y <= max(s.anchor.Y, s.end.Y); y++ {
		var line strings.Builder
		for x := 0; x < s.area.Dx(); {
			cell := s.buffer.CellAt(x, y)
			width, content := 1, " "
			if cell != nil {
				width, content = max(1, cell.Width), cell.Content
			}
			if s.contains(x, y, width) {
				line.WriteString(content)
			}
			x += width
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return sanitizeTerminalText(strings.Join(lines, "\n"))
}

type uiClipboardResult struct {
	selection *uiTextSelection
	err       error
}

func copyUISelection(selection *uiTextSelection) tea.Cmd {
	text := selection.text()
	if text == "" {
		selection.status = "No text selected. Drag across text to copy it."
		return nil
	}
	selection.status = "Copying selection…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd, err := uiClipboardCommand(ctx, runtime.GOOS, os.Getenv, exec.LookPath)
		if err == nil {
			cmd.Stdin = strings.NewReader(text)
			err = cmd.Run()
		}
		return uiClipboardResult{selection: selection, err: err}
	}
}

func uiClipboardCommand(ctx context.Context, platform string, getenv func(string) string, lookPath func(string) (string, error)) (*exec.Cmd, error) {
	var candidates [][]string
	switch {
	case platform == "darwin":
		candidates = [][]string{{"pbcopy"}}
	case platform == "linux" && getenv("WAYLAND_DISPLAY") != "":
		candidates = [][]string{{"wl-copy"}}
	}
	if platform == "linux" && getenv("DISPLAY") != "" {
		candidates = append(candidates, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	for _, candidate := range candidates {
		path, err := lookPath(candidate[0])
		if err == nil {
			return exec.CommandContext(ctx, path, candidate[1:]...), nil
		}
	}
	return nil, fmt.Errorf("no clipboard command available")
}

func (m *uiTaskModel) beginSelection(x, y int) {
	left, width, height := m.size()
	task := left > 0 && x < left || left == 0 && m.taskFocus
	area := image.Rect(left+2, 2, left+2+width, 2+height)
	var content string
	if task {
		if left > 0 {
			width = left - 4
		}
		area = image.Rect(2, 2, 2+width, 2+height)
		content = m.taskPage(width, height)
	} else if len(m.tabs) > 0 {
		tab := m.tabs[m.active]
		tab.mu.Lock()
		if !tab.disposed {
			content = uiTerminalScreen(tab.emulator, tab.scroll, false)
		}
		tab.mu.Unlock()
	}
	if image.Pt(x, y).In(area) && content != "" {
		m.selection = newUITextSelection(content, area, x, y, task)
	}
}
