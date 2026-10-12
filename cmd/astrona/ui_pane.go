package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

const uiPaneEnv = "ASTRONA_INTERNAL_UI_PANE"

// Parse with Cobra so --ui=false, flag values and arguments after -- retain
// their usual meaning. The child owns all command gates and data locks.
func wantsUIPane(args []string) bool {
	root := newRootCmd(&rootFlags{})
	cmd, rest, err := root.Find(args)
	if err != nil {
		return false
	}
	cmd.InitDefaultHelpFlag()
	if err := cmd.ParseFlags(rest); err != nil {
		return false
	}
	if help, _ := cmd.Flags().GetBool("help"); help {
		return false
	}
	if cmd.Name() == "ui" {
		return true
	}
	ui, _ := cmd.Flags().GetBool("ui")
	startup, _ := cmd.Flags().GetBool("ui-startup")
	return cmd.Name() == "run" && (ui || startup)
}

// Titles are interpreted by the embedded terminal, never passed through to
// the user's terminal. This marker grants no terms, trust or restart consent.
func uiPaneTitle(title string) {
	if os.Getenv(uiPaneEnv) == "1" {
		fmt.Fprintf(os.Stdout, "\x1b]2;%s\x07", sanitizeLine(title))
	}
}

func runUIPane(ctx context.Context, args []string) (int, error) {
	if err := requireWorkspaceTerminal(); err != nil {
		return 1, err
	}
	path, err := os.Executable()
	if err != nil {
		return 1, err
	}
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 1, fmt.Errorf("read terminal size: %w", err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Env = append(os.Environ(), uiPaneEnv+"=1", "TERM=xterm-256color")
	return runUIPaneCommand(ctx, cmd, w, h)
}

// A real PTY keeps shell job control, terminal editors, passwords and resize
// handling intact. Only the outer program owns the physical alternate screen.
func runUIPaneCommand(ctx context.Context, cmd *exec.Cmd, width, height int, options ...tea.ProgramOption) (int, error) {
	activityDir, err := os.MkdirTemp("", "astrona-ui-activity-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(activityDir)
	activityPath := filepath.Join(activityDir, "current.json")
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, uiActivityEnv+"="+activityPath)
	w, h := uiPaneContentSize(width, height, "STARTUP")
	terminal, err := pty.StartWithSize(cmd, uiPaneWinsize(w, h))
	if err != nil {
		return 1, fmt.Errorf("open UI terminal: %w", err)
	}
	done := make(chan struct{})
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		close(done)
	}()
	defer func() {
		// Closing the PTY hangs up its foreground shell. The CLI has its own
		// process group; detached lab watchdogs and port forwards keep running.
		_ = terminal.Close()
		select {
		case <-done:
		default:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()

	m := newUIPaneModel(terminal, exited, width, height)
	m.activityPath = activityPath
	dashboardCtx, stopDashboard := context.WithCancel(ctx)
	defer stopDashboard()
	m.loadDashboard = func() tea.Msg {
		readCtx, cancel := context.WithTimeout(dashboardCtx, 5*time.Second)
		defer cancel()
		return readUIHome(readCtx)
	}
	inputDone := make(chan struct{})
	go func() {
		// Drain terminal replies as well as keyboard input. Keep draining if
		// the child exits while the final output still contains a query.
		buf := make([]byte, 32*1024)
		for {
			n, err := m.emulator.Read(buf)
			if n > 0 {
				_, _ = terminal.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		close(inputDone)
	}()
	defer func() {
		_ = terminal.Close()
		// Close the pipe before the emulator, so its reader has stopped
		// before Close changes emulator state.
		_ = m.emulator.InputPipe().(io.Closer).Close()
		<-inputDone
		_ = m.emulator.Close()
	}()
	options = append([]tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}, options...)
	_, err = tea.NewProgram(m, options...).Run()
	m.saveActivity(time.Now())
	if m.activityError != "" {
		err = errors.Join(err, errors.New(m.activityError))
	}
	return m.code, err
}

type uiPaneOutput struct {
	data []byte
	err  error
}
type uiPaneExit struct{ err error }
type uiPaneDrainTimeout struct{}

type uiPaneModel struct {
	emulator                        *vt.Emulator
	terminal                        *os.File
	exited                          <-chan error
	width, height, scroll, code     int
	title, failure                  string
	controls, closing, ended, eof   bool
	account                         uiAccountStatus
	activity                        uiActivityClock
	activityPath, activityError     string
	activityTotal, activitySavedOwn time.Duration
	dashboard                       uiHomeSnapshot
	loadDashboard                   tea.Cmd
	background                      string
	animation                       int
}

func uiPaneSize(width, height int) (int, int) {
	return max(1, min(width, 1000)), max(1, min(height-uiHeaderHeight(width, height)-1, 500))
}

func uiPaneWinsize(width, height int) *pty.Winsize {
	if width < 1 || width > 1000 || height < 1 || height > 500 {
		return &pty.Winsize{Cols: 80, Rows: 24}
	}
	return &pty.Winsize{Cols: uint16(width), Rows: uint16(height)}
}

func newUIPaneModel(terminal *os.File, exited <-chan error, width, height int) *uiPaneModel {
	w, h := uiPaneContentSize(width, height, "STARTUP")
	m := &uiPaneModel{terminal: terminal, exited: exited, width: width, height: height, title: "STARTUP"}
	m.emulator = vt.NewEmulator(w, h)
	m.emulator.SetScrollbackSize(2000)
	m.emulator.SetCallbacks(vt.Callbacks{Title: func(title string) { m.title = sanitizeLine(title) }})
	return m
}

func (m *uiPaneModel) read() tea.Msg {
	buf := make([]byte, 32*1024)
	n, err := m.terminal.Read(buf)
	return uiPaneOutput{data: buf[:n], err: err}
}

func (m *uiPaneModel) Init() tea.Cmd {
	return tea.Batch(m.read, readUIAccountStatus, uiActivityTickCmd(), uiLoadingTickCmd(), m.loadDashboard, func() tea.Msg { return uiPaneExit{<-m.exited} })
}

func (m *uiPaneModel) resizeContent() {
	w, h := uiPaneContentSize(m.width, m.height, m.title)
	if m.emulator.Width() == w && m.emulator.Height() == h {
		return
	}
	m.emulator.Resize(w, h)
	if m.terminal != nil {
		if err := pty.Setsize(m.terminal, uiPaneWinsize(w, h)); err != nil && !m.ended {
			m.failure = "Could not resize terminal: " + err.Error()
		}
	}
}

func (m *uiPaneModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case uiLoadingTick:
		m.animation++
		return m, uiLoadingTickCmd()
	case uiHomeSnapshot:
		m.dashboard = msg
	case uiActivityTick:
		m.activityTick(time.Time(msg))
		return m, uiActivityTickCmd()
	case uiAccountStatus:
		m.account = msg
		return m, uiAccountTick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeContent()
	case uiPaneOutput:
		_, _ = m.emulator.Write(msg.data)
		m.resizeContent()
		if m.title == "HOME" {
			m.background = m.screen()
		}
		if msg.err != nil {
			m.eof = true
			if !errors.Is(msg.err, io.EOF) && !errors.Is(msg.err, syscall.EIO) && !errors.Is(msg.err, os.ErrClosed) {
				m.failure = "Could not read terminal: " + msg.err.Error()
			}
		} else {
			return m, m.read
		}
	case uiPaneExit:
		m.ended = true
		m.saveActivity(time.Now())
		m.activity.paused = true
		if msg.err != nil {
			m.code = 1
			var exit *exec.ExitError
			if errors.As(msg.err, &exit) && exit.ExitCode() > 0 {
				m.code = exit.ExitCode()
			}
			m.failure = fmt.Sprintf("Command ended (exit %d). Review the command output.", m.code)
			if m.code == uiRunDeclinedExitCode {
				m.failure = "Lab startup cancelled. No lab was started or changed."
			}
		}
		if !m.eof {
			return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return uiPaneDrainTimeout{} })
		}
	case uiPaneDrainTimeout:
		_ = m.terminal.Close()
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	if m.ended && m.eof && m.code == 0 && m.failure == "" {
		return m, tea.Quit
	}
	return m, nil
}

func (m *uiPaneModel) key(key tea.KeyMsg) tea.Cmd {
	now := time.Now()
	m.activity.update(now)
	if m.activity.paused && !m.controls && !m.closing && !m.ended && key.Type != tea.KeyCtrlCloseBracket {
		if key.Type == tea.KeyEnter && !key.Paste {
			m.activity.input(now)
			m.saveActivity(now)
		}
		return nil
	}
	if !m.activity.paused {
		m.activity.input(now)
	}
	if m.closing {
		switch key.String() {
		case "y", "Y":
			m.code = 130
			return tea.Quit
		case "n", "N", "enter", "esc", "ctrl+c":
			m.closing = false
		}
		return nil
	}
	if key.Type == tea.KeyCtrlCloseBracket {
		m.controls, m.scroll = !m.controls, 0
		return nil
	}
	if m.ended && (key.String() == "enter" || key.String() == "q" || key.String() == "esc") {
		return tea.Quit
	}
	if m.controls || m.ended {
		switch key.String() {
		case "q":
			m.closing = true
		case "esc", "enter":
			m.controls, m.scroll = false, 0
		case "up", "k":
			m.scroll++
		case "down", "j":
			m.scroll--
		case "pgup":
			m.scroll += m.emulator.Height()
		case "pgdown":
			m.scroll -= m.emulator.Height()
		case "home":
			m.scroll = m.emulator.ScrollbackLen()
		case "end":
			m.scroll = 0
		}
		m.scroll = max(0, min(m.scroll, m.emulator.ScrollbackLen()))
		return nil
	}
	sendUIPaneKey(m.emulator, key)
	return nil
}

func (m *uiPaneModel) View() string {
	content := m.screen()
	if uiLoadingPhase(m.title) {
		content = m.loadingView(content)
	} else if m.title == "START LAB" || m.title == "RESTART LAB" {
		content = m.confirmationView(content)
	}
	header := m.header()
	footer := "Ctrl+] pane controls / scrollback"
	if strings.HasPrefix(m.title, "SHELL /") {
		footer = "Type exit to return to the lab  ·  Ctrl+] pane controls"
	}
	if m.controls {
		footer = "↑/↓ PgUp/PgDn scroll   End latest   Esc resume   q close UI"
	}
	if m.ended {
		footer = "↑/↓ PgUp/PgDn scroll   Enter / Esc close"
	}
	if m.failure != "" {
		header = workspaceFit(header, m.width, uiHeaderHeight(m.width, m.height)-1) + "\n" + workspaceDanger.Render(m.failure)
	}
	if m.activity.paused && !m.controls && !m.ended {
		content = m.idleView(content)
		footer = "Enter: continue · Ctrl+] pane controls"
	}
	if m.activityError != "" {
		footer = m.activityError
	} else if m.activity.target.Lab != "" {
		state := "Active"
		if m.activity.paused {
			state = "Paused"
		}
		footer = "Time spent: " + formatUptime(m.timeSpent()) + " · " + state + " · " + footer
	}
	if m.closing {
		content = workspaceDialog(content, "CLOSE UI", "Close the UI and stop its active command?\nAny lab already created stays on this machine.\n\ny close / Enter or Esc return", m.width, m.emulator.Height(), false)
	}
	_, contentHeight := uiPaneSize(m.width, m.height)
	return workspaceFit(header+"\n"+workspaceFit(content, m.width, contentHeight)+"\n"+workspaceMuted.Render(footer), m.width, m.height)
}

func (m *uiPaneModel) screen() string {
	w, h := m.emulator.Width(), m.emulator.Height()
	buf := uv.NewRenderBuffer(w, h)
	start := m.emulator.ScrollbackLen() - m.scroll
	cursor := m.emulator.CursorPosition()
	for y := range h {
		for x := 0; x < w; {
			var cell *uv.Cell
			if start+y < m.emulator.ScrollbackLen() {
				cell = m.emulator.ScrollbackCellAt(x, start+y)
			} else {
				cell = m.emulator.CellAt(x, start+y-m.emulator.ScrollbackLen())
			}
			c := uv.EmptyCell
			if cell != nil {
				c = *cell
			}
			c.Link = uv.Link{}
			if !m.controls && !m.ended && m.scroll == 0 && !m.emulator.CursorHidden() && cursor.X == x && cursor.Y == y {
				c.Style.Attrs ^= uv.AttrReverse
			}
			buf.SetCell(x, y, &c)
			x += max(1, c.Width)
		}
	}
	return buf.Render()
}

func sendUIPaneKey(emulator *vt.Emulator, key tea.KeyMsg) {
	if key.Paste {
		emulator.Paste(string(key.Runes))
		return
	}
	if key.Type >= 0 && key.Type <= 127 {
		text := string(rune(key.Type))
		if key.Alt {
			text = "\x1b" + text
		}
		emulator.SendText(text)
		return
	}
	if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
		text := string(key.Runes)
		if key.Type == tea.KeySpace {
			text = " "
		}
		if key.Alt {
			text = "\x1b" + text
		}
		emulator.SendText(text)
		return
	}
	special := map[string]rune{"up": uv.KeyUp, "down": uv.KeyDown, "left": uv.KeyLeft, "right": uv.KeyRight, "home": uv.KeyHome, "end": uv.KeyEnd, "pgup": uv.KeyPgUp, "pgdown": uv.KeyPgDown, "delete": uv.KeyDelete, "insert": uv.KeyInsert, "tab": uv.KeyTab}
	k := uv.KeyPressEvent{}
	for _, part := range strings.Split(key.String(), "+") {
		switch part {
		case "ctrl":
			k.Mod |= uv.ModCtrl
		case "alt":
			k.Mod |= uv.ModAlt
		case "shift":
			k.Mod |= uv.ModShift
		default:
			k.Code = special[part]
			if n, err := strconv.Atoi(strings.TrimPrefix(part, "f")); err == nil && n >= 1 && n <= 20 {
				k.Code = uv.KeyF1 + rune(n-1)
			}
		}
	}
	if k.Code != 0 {
		emulator.SendKey(k)
	}
}
