package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// A tab keeps consuming output while Home is visible. Its bounded emulator
// retains the screen and scrollback without counting output as user activity.
type uiTerminalTab struct {
	label                       string
	alias                       string
	mu                          sync.Mutex
	id                          int
	terminal                    *os.File
	emulator                    *vt.Emulator
	cmd                         *exec.Cmd
	done, inputDone, outputDone chan struct{}
	releaseOnce                 sync.Once
	disposed                    bool
	ended                       bool
	failure                     string
	scroll                      int
}

type uiTerminalChanged struct{}
type uiTerminalStarted struct{ err error }

type uiTerminals struct {
	compactPrompt  bool
	taskWidth      int
	savedTaskWidth int
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	tabs           []*uiTerminalTab
	changed        chan struct{}
	workers        sync.WaitGroup
	factory        func(string) (*exec.Cmd, error)
	closeOnce      sync.Once
	active         int
	nextID         int
}

func newUITerminals(ctx context.Context, factory func(string) (*exec.Cmd, error)) *uiTerminals {
	ctx, cancel := context.WithCancel(ctx)
	return &uiTerminals{ctx: ctx, cancel: cancel, factory: factory, changed: make(chan struct{}, 1)}
}

func (t *uiTerminals) notify() {
	select {
	case t.changed <- struct{}{}:
	default:
	}
}

func (t *uiTerminals) next(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-t.changed:
			return uiTerminalChanged{}
		case <-ctx.Done():
			return nil
		}
	}
}

func (t *uiTerminals) snapshot() []*uiTerminalTab {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*uiTerminalTab(nil), t.tabs...)
}

func (t *uiTerminals) start(alias string, width, height int) tea.Cmd {
	return t.startNamed(alias, "", width, height)
}

func (t *uiTerminals) startNamed(alias, label string, width, height int) tea.Cmd {
	return func() tea.Msg {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.ctx.Err() != nil {
			return uiTerminalStarted{err: t.ctx.Err()}
		}
		if len(t.tabs) >= 12 {
			return uiTerminalStarted{err: fmt.Errorf("this workspace already has 12 terminal tabs")}
		}
		cmd, err := t.factory(alias)
		if err != nil {
			return uiTerminalStarted{err: err}
		}
		cleanup := func() {}
		if t.compactPrompt {
			cleanup, err = configureUITerminalPrompt(cmd)
			if err != nil {
				return uiTerminalStarted{err: err}
			}
		}
		terminal, err := pty.StartWithSize(cmd, uiPaneWinsize(width, height))
		if err != nil {
			cleanup()
			return uiTerminalStarted{err: err}
		}
		t.nextID++
		if label == "" {
			label = fmt.Sprintf("Terminal %d", t.nextID)
			if alias != "" {
				label = fmt.Sprintf("%s %d", alias, t.nextID)
			}
		}
		tab := &uiTerminalTab{label: label, alias: alias, id: t.nextID, terminal: terminal, cmd: cmd, emulator: vt.NewEmulator(width, height), done: make(chan struct{}), inputDone: make(chan struct{}), outputDone: make(chan struct{})}
		tab.emulator.SetScrollbackSize(2000)
		t.tabs = append(t.tabs, tab)
		t.workers.Go(func() {
			defer close(tab.inputDone)
			copyUITerminalInput(terminal, tab.emulator, func(err error) {
				tab.mu.Lock()
				tab.failure = "Terminal input stopped: " + err.Error()
				tab.mu.Unlock()
				t.notify()
			})
		})
		t.workers.Go(func() {
			defer cleanup()
			err := cmd.Wait()
			tab.mu.Lock()
			tab.ended = true
			if err != nil {
				tab.failure = err.Error()
			}
			tab.mu.Unlock()
			close(tab.done)
			tab.release()
			t.mu.Lock()
			t.tabs = slices.DeleteFunc(t.tabs, func(candidate *uiTerminalTab) bool { return candidate == tab })
			t.mu.Unlock()
			t.notify()
		})
		t.workers.Go(func() {
			defer close(tab.outputDone)
			for {
				buf := make([]byte, 32*1024)
				n, err := terminal.Read(buf)
				tab.mu.Lock()
				if n > 0 {
					_, _ = tab.emulator.Write(buf[:n])
				}
				if err != nil {
					tab.ended = true
					if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EIO) && !errors.Is(err, os.ErrClosed) {
						tab.failure = err.Error()
					}
				}
				tab.mu.Unlock()
				t.notify()
				if err != nil {
					return
				}
			}
		})
		t.notify()
		return uiTerminalStarted{}
	}
}

// Drain emulator input independently of PTY backpressure. A blocked shell must
// not hold the emulator lock or prevent the UI from processing navigation.
func copyUITerminalInput(terminal io.Writer, emulator *vt.Emulator, report func(error)) {
	queue := make(chan []byte, 256) // At most 8 MiB of pending input.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for data := range queue {
			if _, err := terminal.Write(data); err != nil {
				_ = emulator.InputPipe().(io.Closer).Close()
				report(err)
				// Keep draining until the emulator reader closes the queue.
				for range queue {
				}
				return
			}
		}
	}()
	_, err := io.Copy(terminalInputQueue{queue}, emulator)
	_ = emulator.InputPipe().(io.Closer).Close()
	if err != nil {
		report(err)
	}
	close(queue)
	<-writerDone
}

type terminalInputQueue struct{ queue chan<- []byte }

func (q terminalInputQueue) Write(data []byte) (int, error) {
	select {
	case q.queue <- append([]byte(nil), data...):
		return len(data), nil
	default:
		return 0, fmt.Errorf("shell is not reading input; reopen the terminal")
	}
}

func (tab *uiTerminalTab) release() {
	tab.releaseOnce.Do(func() {
		_ = tab.terminal.Close()
		_ = tab.emulator.InputPipe().(io.Closer).Close()
		<-tab.inputDone
		<-tab.outputDone
		tab.mu.Lock()
		tab.disposed = true
		_ = tab.emulator.Close()
		tab.mu.Unlock()
	})
}

func (t *uiTerminals) close() {
	t.closeOnce.Do(func() {
		t.cancel()
		for _, tab := range t.snapshot() {
			_ = tab.terminal.Close()
			select {
			case <-tab.done:
			default:
				_ = syscall.Kill(-tab.cmd.Process.Pid, syscall.SIGKILL)
				<-tab.done
			}
			tab.release()
		}
		t.workers.Wait()
	})
}

// The pool belongs to one UI invocation and survives visits to Home.
type uiTerminalPoolKey struct{}
type uiTerminalPool struct {
	ctx  context.Context
	runs map[string]uiTerminalRun
}
type uiTerminalRun struct {
	runID     string
	terminals *uiTerminals
}

func uiTerminalContext(ctx context.Context) (context.Context, func()) {
	if ctx.Value(uiTerminalPoolKey{}) != nil {
		return ctx, func() {}
	}
	pool := &uiTerminalPool{ctx: ctx, runs: map[string]uiTerminalRun{}}
	return context.WithValue(ctx, uiTerminalPoolKey{}, pool), func() {
		for _, run := range pool.runs {
			run.terminals.close()
		}
	}
}

func (p *uiTerminalPool) get(m workspaceModel) *uiTerminals {
	if run, ok := p.runs[m.name]; ok {
		if run.runID == m.runID {
			return run.terminals
		}
		run.terminals.close()
	}
	terminals := newUITerminals(p.ctx, m.terminalCommand)
	terminals.compactPrompt = m.taskTerminals
	p.runs[m.name] = uiTerminalRun{runID: m.runID, terminals: terminals}
	return terminals
}

func closeUITerminals(ctx context.Context, name string) {
	if pool, ok := ctx.Value(uiTerminalPoolKey{}).(*uiTerminalPool); ok {
		if run, ok := pool.runs[name]; ok {
			run.terminals.close()
			delete(pool.runs, name)
		}
	}
}

type uiTaskModel struct {
	selection           *uiTextSelection
	taskWidth           int
	dragging            bool
	namingTab           bool
	tabName             string
	lastReplacement     time.Time
	workspace           workspaceModel
	terminals           *uiTerminals
	tabs                []*uiTerminalTab
	active              int
	starting, taskFocus bool
	next                tea.Cmd
	sized               bool
	markdownSource      string
	markdownWidth       int
	markdownLines       []string
}

func (m *uiTaskModel) size() (left, width, height int) {
	w, h := max(8, m.workspace.width), max(6, m.workspace.height)
	percent := m.taskWidth
	if percent == 0 {
		percent = 30
	}
	left = min(w-32, max(18, w*percent/100))
	if w < 64 {
		left = 0
	}
	return left, max(1, w-left-4), max(1, h-5)
}

func (m *uiTaskModel) start() tea.Cmd {
	if m.starting || !m.sized {
		return nil
	}
	if m.workspace.snapshot.blocked != "" {
		return nil
	}
	m.starting = true
	_, w, h := m.size()
	alias := ""
	if m.workspace.selected < len(m.workspace.snapshot.clusters) {
		alias = m.workspace.snapshot.clusters[m.workspace.selected].alias
	}
	return m.terminals.startNamed(alias, m.tabName, w, h)
}

func (m *uiTaskModel) Init() tea.Cmd {
	return tea.Batch(m.next, workspaceTickCmd(), m.workspace.refresh)
}

func (m *uiTaskModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case uiTerminalStarted:
		m.starting = false
		if msg.err != nil {
			m.workspace.notice = "Could not open terminal: " + msg.err.Error()
		}
		return m, nil
	case uiTerminalChanged:
		tabs := m.terminals.snapshot()
		lostLast := len(m.tabs) > 0 && len(tabs) == 0
		var selected *uiTerminalTab
		if m.active < len(m.tabs) {
			selected = m.tabs[m.active]
		}
		for i, tab := range tabs {
			if tab == selected {
				m.active = i
			}
			if !slices.Contains(m.tabs, tab) {
				m.active = i
			}
		}
		m.tabs = tabs
		m.active = min(m.active, max(0, len(tabs)-1))
		if m.active >= len(tabs) || tabs[m.active] != selected {
			m.selection = nil
		}
		m.resize()
		cleanExit := lostLast && selected != nil && selected.cmd != nil && selected.cmd.ProcessState != nil && selected.cmd.ProcessState.Success()
		if lostLast && !m.starting && (cleanExit || time.Since(m.lastReplacement) >= 3*time.Second) {
			m.lastReplacement = time.Now()
			m.tabName = ""
			if selected != nil {
				for i, target := range m.workspace.snapshot.clusters {
					if target.alias == selected.alias {
						m.workspace.selected = i
						break
					}
				}
			}
			return m, tea.Batch(m.next, m.start())
		}
		return m, m.next
	case tea.WindowSizeMsg:
		m.selection = nil
		m.dragging = false
		m.workspace.width, m.workspace.height = msg.Width, msg.Height
		firstSize := !m.sized
		m.sized = msg.Width > 0 && msg.Height > 0
		m.resize()
		if firstSize && len(m.tabs) == 0 {
			return m, m.start()
		}
	case workspaceSnapshot:
		m.workspace.snapshot, m.workspace.polling = msg, false
		m.workspace.nextRefresh = time.Now().Add(5 * time.Second)
		if msg.blocked != "" {
			m.workspace.action = workspaceLeave
			return m, tea.Quit
		}
	case workspaceTick:
		m.workspace.now = time.Time(msg)
		if !m.workspace.polling && m.workspace.refresh != nil && !m.workspace.now.Before(m.workspace.nextRefresh) {
			m.workspace.polling = true
			return m, tea.Batch(workspaceTickCmd(), m.workspace.refresh)
		}
		return m, workspaceTickCmd()
	case uiClipboardResult:
		if m.selection != nil && m.selection == msg.selection {
			m.selection.status = "Copied to clipboard · Esc clears selection"
			if msg.err != nil {
				m.selection.status = "Copy failed · Ctrl+Y: native copy"
			}
		}
	case tea.MouseMsg:
		return m, m.mouse(msg)
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *uiTaskModel) resize() {
	_, w, h := m.size()
	for _, tab := range m.tabs {
		tab.mu.Lock()
		if tab.disposed || (tab.emulator.Width() == w && tab.emulator.Height() == h) {
			tab.mu.Unlock()
			continue
		}
		tab.emulator.Resize(w, h)
		if !tab.ended {
			if err := pty.Setsize(tab.terminal, uiPaneWinsize(w, h)); err != nil {
				m.workspace.notice = "Could not resize terminal: " + err.Error()
			}
		}
		tab.mu.Unlock()
	}
}

type uiTabHit struct{ start, end, index int }

func (m *uiTaskModel) navigation() (string, []uiTabHit) {
	left, w, _ := m.size()
	nav := ""
	if left > 0 {
		nav = workspaceFit(" Ctrl+O: task / terminal", left, 1)
	}
	visible := max(1, (w+4)/22)
	first := max(0, m.active-visible+1)
	var hits []uiTabHit
	for i := first; i < min(len(m.tabs), first+visible); i++ {
		if i > first {
			nav += " "
		}
		start := ansi.StringWidth(nav)
		selected := -1
		if i == m.active {
			selected = 0
		}
		nav += workspaceNav([]string{ansi.Truncate(sanitizeLine(m.tabs[i].label), 16, "…")}, []string{fmt.Sprint(i + 1)}, selected)
		hits = append(hits, uiTabHit{start, min(m.workspace.width, ansi.StringWidth(nav)), i})
	}
	if m.starting {
		nav += " Opening terminal…"
	} else if len(m.tabs) == 0 {
		nav += workspaceNav([]string{"Terminal · retry"}, []string{"1"}, 0)
	}
	return nav, hits
}

func (m *uiTaskModel) taskPage(width, height int) string {
	if m.markdownLines == nil || m.markdownSource != m.workspace.task || m.markdownWidth != width {
		rendered := renderMarkdown(sanitizeTerminalText(m.workspace.task), true)
		if os.Getenv("NO_COLOR") != "" {
			rendered = ansi.Strip(rendered)
		}
		m.markdownSource, m.markdownWidth = m.workspace.task, width
		m.markdownLines = strings.Split(ansi.Wrap(rendered, max(1, width), ""), "\n")
	}
	start := min(max(0, m.workspace.scroll), max(0, len(m.markdownLines)-height))
	return strings.Join(m.markdownLines[start:min(len(m.markdownLines), start+max(0, height))], "\n")
}

func (m *uiTaskModel) View() string {
	left, w, h := m.size()
	nav, _ := m.navigation()
	screen := "Terminal unavailable. Press Enter to reconnect, or Ctrl+T to choose another terminal."
	terminalStatus := ""
	if len(m.tabs) > 0 {
		tab := m.tabs[m.active]
		tab.mu.Lock()
		if !tab.disposed {
			screen = uiTerminalScreen(tab.emulator, tab.scroll, !m.taskFocus && !tab.ended)
		}
		if tab.failure != "" {
			terminalStatus = tab.failure
		}
		if tab.ended {
			terminalStatus = "Shell closed. Ctrl+T opens another terminal."
			if tab.failure != "" {
				terminalStatus += " " + tab.failure
			}
		}
		tab.mu.Unlock()
	}
	if m.selection != nil && !m.selection.task {
		screen = m.selection.render()
	}
	terminal := workspacePanel("TERMINALS", screen, w+4, h+2, workspaceFocus)
	taskStyle := workspaceBorder
	if m.taskFocus {
		taskStyle = workspaceFocus
	}

	body := terminal
	taskWidth := w
	if left > 0 {
		taskWidth = left - 4
	}
	taskPage := m.taskPage(taskWidth, h)
	if m.selection != nil && m.selection.task {
		taskPage = m.selection.render()
	}
	if left > 0 {
		task := workspacePanel("QUESTION / TASK", taskPage, left, h+2, taskStyle)
		body = lipgloss.JoinHorizontal(lipgloss.Top, task, terminal)
	} else if m.taskFocus {
		body = workspacePanel("QUESTION / TASK", taskPage, w+4, h+2, taskStyle)
	}
	footer := "Ctrl+T new · Ctrl+N/P tabs · Ctrl+O task · Ctrl+G lab · Ctrl+Y copy · Ctrl+B overview"
	if terminalStatus != "" {
		footer = terminalStatus
	}
	if m.workspace.notice != "" {
		footer = m.workspace.notice
	}
	if m.selection != nil {
		footer = "Drag to select · Release to copy · Esc clears selection"
		if m.selection.status != "" {
			footer = m.selection.status
		}
	}
	view := workspaceFit(nav+"\n"+body+"\n"+workspaceMuted.Render(sanitizeLine(strings.TrimSpace(footer)))+"\n"+workspaceMuted.Render("Drag text to copy · Ctrl+C interrupts a command · Ctrl+B pauses time"), m.workspace.width, m.workspace.height)
	if m.namingTab {
		text := "Name: " + sanitizeLine(m.tabName) + "_\n\nEnter opens the tab; leave blank for a default name.\nSwitch tabs with a mouse click or Ctrl+N / Ctrl+P.\n\nEnter opens · Esc cancels · Ctrl+U clears"
		if len(m.workspace.snapshot.clusters) > 0 && m.workspace.snapshot.clusters[0].runtime == "qemu" {
			target := m.workspace.snapshot.clusters[m.workspace.selected]
			text = "VM: " + sanitizeLine(target.name) + " (←/→ choose)\n" + text
		}
		if m.workspace.width < 48 || m.workspace.height < 14 {
			text = "Resize to 48 × 14 to name a terminal. Esc cancels."
		}
		return workspaceDialog(view, "NEW TERMINAL", text, m.workspace.width, m.workspace.height, false)
	}
	return view
}

func (m *uiTaskModel) nameTab(key tea.KeyMsg) tea.Cmd {
	if !key.Paste {
		switch key.String() {
		case "left", "right":
			targets := m.workspace.snapshot.clusters
			if len(targets) > 0 && targets[0].runtime == "qemu" {
				step := 1
				if key.String() == "left" {
					step = -1
				}
				m.workspace.selected = (m.workspace.selected + step + len(targets)) % len(targets)
			}
			return nil
		case "esc", "ctrl+c":
			m.namingTab = false
			return nil
		case "enter":
			if m.workspace.width < 48 || m.workspace.height < 14 {
				return nil
			}
			m.tabName = strings.TrimSpace(m.tabName)
			m.namingTab = false
			return m.start()
		case "backspace", "ctrl+h":
			runes := []rune(m.tabName)
			if len(runes) > 0 {
				m.tabName = string(runes[:len(runes)-1])
			}
			return nil
		case "ctrl+u":
			m.tabName = ""
			return nil
		}
	}
	if key.Type == tea.KeyRunes {
		for _, r := range key.Runes {
			if unicode.IsPrint(r) && len([]rune(m.tabName)) < 24 {
				m.tabName += string(r)
			}
		}
	}
	return nil
}

func runUITaskScreen(ctx context.Context, workspace workspaceModel, refresh func(context.Context) workspaceSnapshot) (workspaceModel, error) {
	uiPaneTitle("TASK / " + workspace.name)
	screenCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx, closePool := uiTerminalContext(ctx)
	defer closePool()
	terminals := ctx.Value(uiTerminalPoolKey{}).(*uiTerminalPool).get(workspace)
	path := workspace.settingsPath
	if path == "" {
		var err error
		path, err = settingsPath(&rootFlags{})
		if err != nil {
			return workspace, err
		}
	}
	saved, err := loadSettings(path)
	if err != nil {
		return workspace, err
	}
	value, err := saved.value("ui-task-width")
	if err != nil {
		return workspace, err
	}
	percent, err := strconv.Atoi(string(value))
	if err != nil {
		return workspace, err
	}
	if terminals.savedTaskWidth != percent || terminals.taskWidth == 0 {
		terminals.taskWidth, terminals.savedTaskWidth = percent, percent
	}
	workspace.action = workspaceNone
	if refresh != nil {
		workspace.refresh = func() tea.Msg { return refresh(screenCtx) }
		workspace.polling = true
	}
	m := &uiTaskModel{taskWidth: terminals.taskWidth, workspace: workspace, terminals: terminals, tabs: terminals.snapshot(), active: 0, next: terminals.next(screenCtx)}
	for i, tab := range m.tabs {
		if tab.id == terminals.active {
			m.active = i
		}
	}
	_, err = runUIProgram(m, tea.WithContext(screenCtx), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if m.active < len(m.tabs) {
		terminals.active = m.tabs[m.active].id
	} else {
		terminals.active = 0
	}
	if m.workspace.snapshot.blocked != "" {
		closeUITerminals(ctx, workspace.name)
	}
	return m.workspace, err
}
