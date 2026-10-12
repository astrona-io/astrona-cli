package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type workspaceAction int

const (
	workspaceNone workspaceAction = iota
	workspaceLeave
	workspaceShell
	workspaceSubmit
	workspaceDestroy
	workspaceSelect
	workspaceLoadPreferences
	workspaceSavePreference
)

type workspaceCluster struct {
	name, alias, status string
}

type workspaceSnapshot struct {
	clusters                        []workspaceCluster
	started, deadline               time.Time
	kind, tracking, result, blocked string
}

type workspaceTick time.Time

// workspaceModel owns display state only. Lifecycle actions run after the
// program has stopped reading stdin and restored the terminal.
type workspaceModel struct {
	name, task, notice                                    string
	submission                                            string
	snapshot                                              workspaceSnapshot
	labs                                                  []string
	selected, scroll, width, height                       int
	results, exitMenu, confirmDestroy, canSubmit, polling bool
	action                                                workspaceAction
	now                                                   time.Time
	nextRefresh                                           time.Time
	refresh                                               func() tea.Msg
	showPreferences                                       bool
	preferences                                           workspacePreferences
}

func workspaceTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return workspaceTick(t) })
}

func (m workspaceModel) Init() tea.Cmd {
	if len(m.labs) > 0 {
		return nil
	}
	return tea.Batch(workspaceTickCmd(), m.refresh)
}

func (m workspaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case workspaceSnapshot:
		m.snapshot, m.polling = msg, false
		m.nextRefresh = time.Now().Add(5 * time.Second)
		m.selected = min(m.selected, max(0, len(msg.clusters)-1))
	case workspaceTick:
		m.now = time.Time(msg)
		if !m.polling && m.refresh != nil && !m.now.Before(m.nextRefresh) {
			m.polling = true
			return m, tea.Batch(workspaceTickCmd(), m.refresh)
		}
		return m, workspaceTickCmd()
	case tea.KeyMsg:
		key := msg.String()
		if len(m.labs) > 0 {
			switch key {
			case "up", "k":
				m.selected = max(0, m.selected-1)
			case "down", "j":
				m.selected = min(len(m.labs)-1, m.selected+1)
			case "enter":
				m.action = workspaceSelect
				return m, tea.Quit
			case "q", "esc", "ctrl+c", "ctrl+d":
				m.action = workspaceLeave
				return m, tea.Quit
			}
			return m, nil
		}
		if m.showPreferences {
			return m.updatePreferences(msg)
		}
		if m.confirmDestroy {
			switch key {
			case "y":
				if m.snapshot.blocked == "" {
					m.action = workspaceDestroy
					return m, tea.Quit
				}
			case "n", "esc", "enter", "q", "ctrl+c":
				m.confirmDestroy = false
			}
			return m, nil
		}
		if m.exitMenu {
			switch key {
			case "k":
				m.action = workspaceLeave
				return m, tea.Quit
			case "esc", "enter", "q", "ctrl+c":
				m.exitMenu = false
			case "d":
				if m.snapshot.blocked == "" {
					m.confirmDestroy = true
				}
			case "s":
				if m.canSubmit && m.snapshot.blocked == "" {
					m.action = workspaceSubmit
					return m, tea.Quit
				}
			}
			return m, nil
		}
		switch key {
		case "p":
			m.action = workspaceLoadPreferences
			return m, tea.Quit
		case "q", "esc", "ctrl+c", "ctrl+d":
			m.exitMenu = true
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "down", "j":
			m.selected = min(max(0, len(m.snapshot.clusters)-1), m.selected+1)
		case "tab":
			m.results = !m.results
			m.scroll = 0
		case "pgdown":
			m.scroll += max(1, m.height/2)
		case "pgup":
			m.scroll = max(0, m.scroll-max(1, m.height/2))
		case "home":
			m.scroll = 0
		case "enter", "s":
			if m.snapshot.blocked == "" && len(m.snapshot.clusters) > 0 {
				m.action = workspaceShell
				return m, tea.Quit
			}
		case "g":
			if m.canSubmit && m.snapshot.blocked == "" {
				m.action = workspaceSubmit
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m workspaceModel) View() string {
	w, h := max(20, m.width-4), max(5, m.height-2)
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	clip := func(s string) string { return ansi.Truncate(sanitizeLine(s), w, "…") }
	heading := accent.Render("ASTRONA  /  LAB WORKSPACE")
	if len(m.labs) > 0 {
		lines := []string{heading, "", "Choose a running Kind lab", ""}
		start := max(0, m.selected-max(1, h-9)+1)
		for i := start; i < min(len(m.labs), start+max(1, h-9)); i++ {
			prefix := "  "
			if i == m.selected {
				prefix = "> "
			}
			lines = append(lines, clip(prefix+m.labs[i]))
		}
		return strings.Join(append(lines, "", "↑/↓ select   enter open   q cancel"), "\n")
	}
	if m.showPreferences {
		return m.preferencesView()
	}
	if m.width < 48 || m.height < 14 {
		return heading + "\nTerminal too small (minimum 48 × 14).\n" + m.menuText()
	}
	clock := "Elapsed: unknown (older run)"
	if !m.snapshot.started.IsZero() {
		clock = "Elapsed: " + max(time.Duration(0), m.now.Sub(m.snapshot.started)).Round(time.Second).String()
	}
	if !m.snapshot.deadline.IsZero() {
		remaining := m.snapshot.deadline.Sub(m.now)
		if remaining > 0 {
			clock += "   Left: " + remaining.Round(time.Second).String()
		} else {
			clock += "   Time limit reached"
		}
	}
	lines := []string{heading, clip(m.name + "  /  " + m.snapshot.kind), clip(clock), dim.Render(clip(m.snapshot.tracking))}
	if m.snapshot.blocked != "" {
		lines = append(lines, accent.Render(clip(m.snapshot.blocked)))
	} else {
		lines = append(lines, clip(m.notice))
	}
	leftWidth := min(36, max(20, w/3))
	contentWidth := w - leftWidth - 5
	menu := ansi.Wrap(m.menuText(), w, "")
	bodyHeight := max(3, m.height-8-lipgloss.Height(menu))
	left := []string{"CLUSTERS"}
	visible := max(1, (bodyHeight-1)/2)
	start := max(0, m.selected-visible+1)
	for i := start; i < min(len(m.snapshot.clusters), start+visible); i++ {
		c := m.snapshot.clusters[i]
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		label := ansi.Truncate(sanitizeLine(prefix+c.name), leftWidth, "…")
		if i == m.selected {
			label = accent.Render(label)
		}
		left = append(left, label, dim.Render(ansi.Truncate("  "+sanitizeLine(c.status), leftWidth, "…")))
	}
	content, title := m.task, "THE TASK"
	if m.results {
		content, title = m.snapshot.result, "SUBMISSION RESULTS"
		if m.submission != "" {
			content = m.submission
		}
	}
	if content == "" {
		content = "No task document provided."
	}
	wrapped := strings.Split(ansi.Wrap(sanitizeTerminalText(content), max(10, contentWidth), ""), "\n")
	pageSize := max(1, bodyHeight-1)
	offset := min(m.scroll, max(0, len(wrapped)-pageSize))
	right := append([]string{title}, wrapped[offset:min(len(wrapped), offset+pageSize)]...)
	leftPanel := lipgloss.NewStyle().Width(leftWidth).Height(bodyHeight).Render(strings.Join(left, "\n"))
	rightPanel := lipgloss.NewStyle().Width(contentWidth).Height(bodyHeight).Render(strings.Join(right, "\n"))
	divider := dim.Render(strings.TrimSuffix(strings.Repeat("  │  \n", bodyHeight), "\n"))
	lines = append(lines, "", lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, divider, rightPanel), "", menu)
	return strings.Join(lines, "\n")
}

func (m workspaceModel) menuText() string {
	if m.confirmDestroy {
		return "Destroy this lab and its linked clusters?\nAll work inside them is lost.\ny confirm / enter cancel"
	}
	if m.exitMenu {
		choices := "k leave running   d destroy   enter return"
		if m.canSubmit {
			choices = "s submit   " + choices
		}
		return "Leave workspace?\n" + choices
	}
	keys := "↑/↓ cluster   s shell   tab task/results   pgup/pgdn scroll   p preferences"
	if m.canSubmit {
		keys += "\ng submit   q exit menu"
	} else {
		keys += "\nq exit menu"
	}
	return keys
}

// runWorkspaceScreen owns the polling context. Returning to a shell or grading
// first cancels in-flight reads; no background refresh writes to the terminal.
func runWorkspaceScreen(ctx context.Context, m workspaceModel, refresh func(context.Context) workspaceSnapshot) (workspaceModel, error) {
	if err := ctx.Err(); err != nil {
		return m, err
	}
	screenCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if refresh != nil {
		m.refresh = func() tea.Msg { return refresh(screenCtx) }
		m.polling = true
	}
	m.action, m.now = workspaceNone, time.Now()
	final, err := tea.NewProgram(m, tea.WithContext(screenCtx), tea.WithAltScreen()).Run()
	if err != nil {
		return m, fmt.Errorf("terminal workspace: %w", err)
	}
	return final.(workspaceModel), nil
}
