package main

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
)

type uiSizeTestModel struct{ width, height int }

func (m uiSizeTestModel) Init() tea.Cmd { return nil }
func (m uiSizeTestModel) View() string  { return fmt.Sprintf("Size: %d x %d", m.width, m.height) }
func (m uiSizeTestModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "q" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func TestUIProgramRecoversWithoutResizeSignal(t *testing.T) {
	// This PTY has no foreground process group, so changing its size does not
	// send SIGWINCH to the UI. It reproduces a missed startup resize signal.
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, uiPaneWinsize(72, 18)); err != nil {
		t.Fatal(err)
	}
	input, keys := io.Pipe()
	defer input.Close()
	defer keys.Close()
	out := &uiPaneTestOutput{}
	go func() { _, _ = io.Copy(out, master) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type result struct {
		model tea.Model
		err   error
	}
	done := make(chan result, 1)
	go func() {
		m, err := runUIProgram(uiSizeTestModel{}, tea.WithInput(input), tea.WithOutput(slave), tea.WithContext(ctx))
		done <- result{m, err}
	}()
	waitUIPaneOutput(t, out, "Size: 72 x 18")
	for _, size := range [][2]int{{140, 45}, {60, 16}, {120, 35}} {
		if err := pty.Setsize(master, uiPaneWinsize(size[0], size[1])); err != nil {
			t.Fatal(err)
		}
		waitUIPaneOutput(t, out, fmt.Sprintf("Size: %d x %d", size[0], size[1]))
	}
	_, _ = io.WriteString(keys, "q")
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.model != (uiSizeTestModel{120, 35}) {
		t.Fatalf("final model: %#v", got.model)
	}
}

func TestUISizeFilterPreservesInteraction(t *testing.T) {
	task := &uiTaskModel{sized: true, dragging: true, workspace: workspaceModel{width: 120, height: 35}}
	m := uiSizedModel{Model: task, width: 120, height: 35}
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 35}, {Width: 0, Height: 0}, {Width: 120, Height: 0}} {
		if msg := uiSizeFilter(m, size); msg != nil {
			t.Fatalf("unnecessary resize: %+v", msg)
		}
	}
	if !task.dragging {
		t.Fatal("size check interrupted dragging")
	}
	key := tea.KeyMsg{Type: tea.KeyCtrlO}
	if uiSizeFilter(m, key) == nil {
		t.Fatal("size filter swallowed input")
	}
	resize := tea.WindowSizeMsg{Width: 140, Height: 45}
	msg := uiSizeFilter(m, resize)
	if msg == nil {
		t.Fatal("real resize was discarded")
	}
	next, _ := m.Update(msg)
	if next.(uiSizedModel).width != 140 || task.workspace.width != 140 || task.dragging {
		t.Fatal("real resize did not reach the task")
	}
}
