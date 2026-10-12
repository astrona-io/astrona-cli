package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// A tab keeps consuming output while Home is visible. Its bounded emulator
// retains the screen and scrollback without counting output as user activity.
type uiTerminalTab struct {
	mu              sync.Mutex
	id              int
	terminal        *os.File
	emulator        *vt.Emulator
	cmd             *exec.Cmd
	done, inputDone chan struct{}
	ended           bool
	failure         string
	scroll          int
}

type uiTerminalChanged struct{}
type uiTerminalStarted struct{ err error }

type uiTerminals struct {
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	tabs      []*uiTerminalTab
	changed   chan struct{}
	workers   sync.WaitGroup
	factory   func(string) (*exec.Cmd, error)
	closeOnce sync.Once
	active    int
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
		terminal, err := pty.StartWithSize(cmd, uiPaneWinsize(width, height))
		if err != nil {
			return uiTerminalStarted{err: err}
		}
		tab := &uiTerminalTab{id: len(t.tabs) + 1, terminal: terminal, cmd: cmd, emulator: vt.NewEmulator(width, height), done: make(chan struct{}), inputDone: make(chan struct{})}
		tab.emulator.SetScrollbackSize(2000)
		t.tabs = append(t.tabs, tab)
		t.workers.Go(func() {
			defer close(tab.inputDone)
			_, _ = io.Copy(terminal, tab.emulator)
		})
		t.workers.Go(func() {
			defer close(tab.done)
			err := cmd.Wait()
			tab.mu.Lock()
			tab.ended = true
			if err != nil {
				tab.failure = err.Error()
			}
			tab.mu.Unlock()
			t.notify()
		})
		t.workers.Go(func() {
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

func (t *uiTerminals) close() {
	t.closeOnce.Do(func() {
		t.cancel()
		t.mu.Lock()
		defer t.mu.Unlock()
		for _, tab := range t.tabs {
			_ = tab.terminal.Close()
			select {
			case <-tab.done:
			default:
				_ = syscall.Kill(-tab.cmd.Process.Pid, syscall.SIGKILL)
				<-tab.done
			}
			_ = tab.emulator.InputPipe().(io.Closer).Close()
			<-tab.inputDone
		}
		t.workers.Wait()
		for _, tab := range t.tabs {
			_ = tab.emulator.Close()
		}
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
	workspace           workspaceModel
	terminals           *uiTerminals
	tabs                []*uiTerminalTab
	active              int
	starting, taskFocus bool
	next                tea.Cmd
}

func (m *uiTaskModel) size() (left, width, height int) {
	w, h := max(8, m.workspace.width), max(6, m.workspace.height)
	left = min(60, max(22, w*2/5))
	if w < 64 {
		left = 0
	}
	return left, max(1, w-left-4), max(1, h-5)
}

func (m *uiTaskModel) start() tea.Cmd {
	if m.starting {
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
	return m.terminals.start(alias, w, h)
}

func (m *uiTaskModel) Init() tea.Cmd {
	var start tea.Cmd
	if len(m.tabs) == 0 {
		start = m.start()
	}
	return tea.Batch(m.next, start, workspaceTickCmd(), m.workspace.refresh)
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
		if len(tabs) > len(m.tabs) {
			m.active = len(tabs) - 1
		}
		m.tabs = tabs
		m.resize()
		return m, m.next
	case tea.WindowSizeMsg:
		m.workspace.width, m.workspace.height = msg.Width, msg.Height
		m.resize()
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
	case tea.KeyMsg:
		if !msg.Paste {
			switch msg.String() {
			case "ctrl+c":
				m.workspace.action = workspaceLeave
				return m, tea.Quit
			case "ctrl+t":
				return m, m.start()
			case "ctrl+n", "ctrl+p":
				if len(m.tabs) > 0 {
					step := 1
					if msg.String() == "ctrl+p" {
						step = len(m.tabs) - 1
					}
					m.active = (m.active + step) % len(m.tabs)
				}
				return m, nil
			case "f2":
				m.taskFocus = !m.taskFocus
				return m, nil
			case "f3":
				m.workspace.action = workspaceSelect
				return m, tea.Quit
			}
		}
		if m.taskFocus {
			switch msg.String() {
			case "down", "j":
				m.workspace.scroll++
			case "up", "k":
				m.workspace.scroll = max(0, m.workspace.scroll-1)
			case "pgdown":
				m.workspace.scroll += max(1, m.workspace.height/2)
			case "pgup":
				m.workspace.scroll = max(0, m.workspace.scroll-max(1, m.workspace.height/2))
			}
			return m, nil
		}
		if len(m.tabs) > 0 {
			tab := m.tabs[m.active]
			tab.mu.Lock()
			defer tab.mu.Unlock()
			if !msg.Paste {
				switch msg.String() {
				case "shift+pgup":
					tab.scroll = min(tab.emulator.ScrollbackLen(), tab.scroll+tab.emulator.Height())
					return m, nil
				case "shift+pgdown":
					tab.scroll = max(0, tab.scroll-tab.emulator.Height())
					return m, nil
				}
			}
			tab.scroll = 0
			if msg.String() == "alt+c" && !msg.Paste {
				msg = tea.KeyMsg{Type: tea.KeyCtrlC}
			}
			if !tab.ended {
				sendUIPaneKey(tab.emulator, msg)
			}
		}
	}
	return m, nil
}

func (m *uiTaskModel) resize() {
	_, w, h := m.size()
	for _, tab := range m.tabs {
		tab.mu.Lock()
		if tab.emulator.Width() == w && tab.emulator.Height() == h {
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

func (m *uiTaskModel) View() string {
	left, w, h := m.size()
	var labels, keys []string
	visible := max(1, (w+4)/22)
	first := max(0, m.active-visible+1)
	for i := first; i < min(len(m.tabs), first+visible); i++ {
		tab := m.tabs[i]
		tab.mu.Lock()
		label := fmt.Sprintf("Terminal %d", tab.id)
		if tab.ended {
			label += " (closed)"
		}
		tab.mu.Unlock()
		labels = append(labels, label)
		keys = append(keys, fmt.Sprint(i+1))
	}
	nav := workspaceNav(labels, keys, m.active-first)
	if left > 0 {
		nav = workspaceFit(" F2: task / terminal", left, 1) + nav
	}
	if m.starting {
		nav += " Opening terminal…"
	}
	screen := "Press Ctrl+T to open a terminal."
	terminalStatus := ""
	if len(m.tabs) > 0 {
		tab := m.tabs[m.active]
		tab.mu.Lock()
		screen = uiTerminalScreen(tab.emulator, tab.scroll, !m.taskFocus && !tab.ended)
		if tab.ended {
			terminalStatus = "Shell closed. Ctrl+T opens another terminal."
			if tab.failure != "" {
				terminalStatus += " " + tab.failure
			}
		}
		tab.mu.Unlock()
	}
	terminal := workspacePanel("TERMINALS", screen, w+4, h+2, workspaceFocus)
	taskStyle := workspaceBorder
	if m.taskFocus {
		taskStyle = workspaceFocus
	}

	body := terminal
	if left > 0 {
		task := workspacePanel("QUESTION / TASK", workspacePage(m.workspace.task, left-4, h, m.workspace.scroll), left, h+2, taskStyle)
		body = lipgloss.JoinHorizontal(lipgloss.Top, task, terminal)
	} else if m.taskFocus {
		body = workspacePanel("QUESTION / TASK", workspacePage(m.workspace.task, w, h, m.workspace.scroll), w+4, h+2, taskStyle)
	}
	footer := "Ctrl+T new · Ctrl+N/P tabs · F2 task · F3 lab · Ctrl+C overview"
	if terminalStatus != "" {
		footer = terminalStatus
	}
	if m.workspace.notice != "" {
		footer = m.workspace.notice
	}
	return workspaceFit(nav+"\n"+body+"\n"+workspaceMuted.Render(sanitizeLine(strings.TrimSpace(footer)))+"\n"+workspaceMuted.Render("Alt+C interrupts a command · Ctrl+C pauses time; tabs keep running"), m.workspace.width, m.workspace.height)
}

func runUITaskScreen(ctx context.Context, workspace workspaceModel, refresh func(context.Context) workspaceSnapshot) (workspaceModel, error) {
	uiPaneTitle("TASK / " + workspace.name)
	screenCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx, closePool := uiTerminalContext(ctx)
	defer closePool()
	terminals := ctx.Value(uiTerminalPoolKey{}).(*uiTerminalPool).get(workspace)
	workspace.action = workspaceNone
	if refresh != nil {
		workspace.refresh = func() tea.Msg { return refresh(screenCtx) }
		workspace.polling = true
	}
	m := &uiTaskModel{workspace: workspace, terminals: terminals, tabs: terminals.snapshot(), active: terminals.active, next: terminals.next(screenCtx)}
	_, err := tea.NewProgram(m, tea.WithContext(screenCtx), tea.WithAltScreen()).Run()
	terminals.active = m.active
	if m.workspace.snapshot.blocked != "" {
		closeUITerminals(ctx, workspace.name)
	}
	return m.workspace, err
}
