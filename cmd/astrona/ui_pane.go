package main

import (
	"astrona/internal/labstate"
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
	if runTestMode(cmd) {
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
	inputErrors := make(chan error, 1)
	m.inputError = func() tea.Msg {
		select {
		case err := <-inputErrors:
			return uiPaneInputError{err}
		case <-dashboardCtx.Done():
			return nil
		}
	}
	go func() {
		defer close(inputDone)
		copyUITerminalInput(terminal, m.emulator, func(err error) {
			select {
			case inputErrors <- err:
			default:
			}
		})
	}()
	defer func() {
		_ = terminal.Close()
		// Close the pipe before the emulator, so its reader has stopped
		// before Close changes emulator state.
		_ = m.emulator.InputPipe().(io.Closer).Close()
		<-inputDone
		_ = m.emulator.Close()
	}()
	options = append([]tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx)}, options...)
	_, err = runUIProgram(m, options...)
	m.activity.update(time.Now())
	m.activity.paused = true
	m.saveActivity(time.Now())
	if m.activityError != "" {
		err = errors.Join(err, errors.New(m.activityError))
	}
	return m.code, err
}

type uiPaneInputError struct{ err error }

type uiPaneOutput struct {
	data []byte
	err  error
}
type uiPaneExit struct{ err error }
type uiPaneDrainTimeout struct{}

type uiPaneModel struct {
	usage                           labstate.ResourceUsage
	mouseDown                       bool
	selectText                      bool
	selectionView                   string
	inputError                      tea.Cmd
	emulator                        *vt.Emulator
	terminal                        *os.File
	exited                          <-chan error
	width, height, scroll, code     int
	title, failure                  string
	controls, closing, ended, eof   bool
	account                         uiAccountStatus
	activity                        uiActivityClock
	activityPath, activityError     string
	activityAway                    bool
	activityTimeFrozen              bool
	idleOverview                    bool
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
	return tea.Batch(m.read, m.inputError, readUIAccountStatus, uiActivityTickCmd(), uiLoadingTickCmd(), m.loadDashboard, func() tea.Msg { return uiPaneExit{<-m.exited} })
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
	case uiPaneInputError:
		if !m.ended {
			m.failure = "Terminal input stopped: " + msg.err.Error()
		}
	case uiLoadingTick:
		m.animation++
		return m, uiLoadingTickCmd()
	case uiHomeSnapshot:
		m.dashboard = msg
	case uiActivityTick:
		return m, tea.Batch(m.activityTick(time.Time(msg)), uiActivityTickCmd())
	case uiAccountStatus:
		m.account = msg
		return m, uiAccountTick()
	case tea.WindowSizeMsg:
		m.mouseDown = false
		m.selectionView = ""
		m.width, m.height = msg.Width, msg.Height
		m.resizeContent()
		if m.selectText {
			m.selectionView = m.View()
		}
	case uiPaneOutput:
		_, _ = m.emulator.Write(msg.data)
		m.resizeContent()
		if m.title == "HOME" {
			m.background = m.screen()
			if m.activity.target.Lab != "" && !m.activityAway {
				m.activity.update(time.Now())
				m.activity.paused = true
				m.activityAway = true
				m.saveActivity(time.Now())
			}
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
		m.activity.update(time.Now())
		m.activity.paused = true
		m.saveActivity(time.Now())
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
	case tea.MouseMsg:
		restoreMouse := m.advanceActivity(time.Now())
		if m.selectText {
			return m, nil
		}
		if !strings.HasPrefix(m.title, "TASK / ") || m.controls || m.closing || m.ended || m.activity.paused || m.activityAway {
			m.mouseDown = false
			return m, restoreMouse
		}
		row := msg.Y - uiHeaderHeight(m.width, m.height)
		mouse := uv.Mouse{X: min(max(0, msg.X), max(0, m.width-1)), Y: min(max(0, row), max(0, m.emulator.Height()-1)), Button: uv.MouseLeft}
		switch msg.Action {
		case tea.MouseActionPress:
			if msg.Button == tea.MouseButtonLeft && row >= 0 && row < m.emulator.Height() {
				m.mouseDown = true
				m.activity.input(time.Now())
				m.emulator.SendMouse(uv.MouseClickEvent(mouse))
			}
		case tea.MouseActionMotion:
			if m.mouseDown && msg.Button == tea.MouseButtonLeft {
				m.activity.input(time.Now())
				m.emulator.SendMouse(uv.MouseMotionEvent(mouse))
			}
		case tea.MouseActionRelease:
			if m.mouseDown {
				m.mouseDown = false
				m.emulator.SendMouse(uv.MouseReleaseEvent(mouse))
			}
		}
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
	return tea.Batch(m.advanceActivity(now), m.handleKey(key, now))
}

func (m *uiPaneModel) handleKey(key tea.KeyMsg, now time.Time) tea.Cmd {
	if !m.activityAway && m.activity.paused && !m.ended {
		return m.idleKey(key, now)
	}
	if !m.activityAway && !m.activity.paused {
		m.activity.input(now)
	}
	if !key.Paste && key.Type == tea.KeyCtrlY {
		m.selectText, m.mouseDown = !m.selectText, false
		m.selectionView = ""
		if m.selectText {
			m.selectionView = m.View()
			return tea.DisableMouse
		}
		return tea.EnableMouseCellMotion
	}
	if m.selectText {
		m.selectText, m.selectionView = false, ""
		if !key.Paste && key.Type == tea.KeyEsc {
			return tea.EnableMouseCellMotion
		}
		return tea.Batch(tea.EnableMouseCellMotion, m.handleKey(key, now))
	}
	if !key.Paste && key.Type == tea.KeyCtrlB && !key.Alt && !m.controls && !m.closing && !m.ended && (strings.HasPrefix(m.title, "TASK / ") || strings.HasPrefix(m.title, "WORKSPACE / ")) {
		m.leaveActivity(now)
		return nil
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
	if m.selectText && m.selectionView != "" {
		return m.selectionView
	}
	content := m.screen()
	if uiLoadingPhase(m.title) {
		content = m.loadingView(content)
	} else if m.title == "START LAB" || m.title == "RESTART LAB" || m.title == "TRACK YOUR PROGRESS" {
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
	if !m.activityAway && m.activity.paused && !m.controls && !m.ended {
		content = m.idleView(content)
		footer = "Enter: confirm · ←/→: Continue / Overview · Esc: overview"
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
	if m.selectText {
		footer = "Select text with the mouse; use your terminal's Copy shortcut · Ctrl+Y restores mouse controls"
	}
	_, contentHeight := uiPaneSize(m.width, m.height)
	return workspaceFit(header+"\n"+workspaceFit(content, m.width, contentHeight)+"\n"+workspaceMuted.Render(footer), m.width, m.height)
}

func (m *uiPaneModel) screen() string {
	return uiTerminalScreen(m.emulator, m.scroll, !m.controls && !m.ended)
}

func uiTerminalScreen(emulator *vt.Emulator, scroll int, showCursor bool) string {
	w, h := emulator.Width(), emulator.Height()
	buf := uv.NewRenderBuffer(w, h)
	start := emulator.ScrollbackLen() - scroll
	cursor := emulator.CursorPosition()
	for y := range h {
		for x := 0; x < w; {
			var cell *uv.Cell
			if start+y < emulator.ScrollbackLen() {
				cell = emulator.ScrollbackCellAt(x, start+y)
			} else {
				cell = emulator.CellAt(x, start+y-emulator.ScrollbackLen())
			}
			c := uv.EmptyCell
			if cell != nil {
				c = *cell
			}
			c.Link = uv.Link{}
			if showCursor && scroll == 0 && !emulator.CursorHidden() && cursor.X == x && cursor.Y == y {
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
