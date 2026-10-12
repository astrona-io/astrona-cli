package main

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	workspaceStart
	workspaceReloadLabs
)

type workspaceCluster struct {
	name, alias, status string
}

type workspaceSnapshot struct {
	clusters                        []workspaceCluster
	started, deadline               time.Time
	kind, tracking, result, blocked string
	activeTime                      time.Duration
	activityTracked                 bool
	activityError                   string
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
	home, enteringLab                                     bool
	labDraft                                              string
	navigation                                            string
	showAlert                                             bool
	alertScroll                                           int
}

func workspaceTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return workspaceTick(t) })
}

func (m workspaceModel) Init() tea.Cmd {
	if m.home || len(m.labs) > 0 {
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
		if m.showAlert {
			switch key {
			case "esc", "enter", "q", "ctrl+c", "ctrl+d":
				m.showAlert = false
			case "down", "j", "pgdown":
				m.alertScroll += 3
			case "up", "k", "pgup":
				m.alertScroll = max(0, m.alertScroll-3)
			}
			return m, nil
		}
		if (key == "f1" || (key == "!" && !m.enteringLab && !m.preferences.editing)) && m.alertMessage() != "" {
			m.showAlert, m.alertScroll = true, 0
			return m, nil
		}
		if m.showPreferences {
			return m.updatePreferences(msg)
		}
		if m.home {
			return m.updateHome(msg)
		}
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
		case "enter":
			m.results, m.scroll = false, 0
		case "s":
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
	if m.width < 48 || m.height < 14 {
		return m.smallView()
	}
	var view string
	if m.showPreferences {
		view = m.preferencesView()
	} else if m.home || len(m.labs) > 0 {
		view = m.homeView()
	} else {
		view = m.labView()
	}
	if m.showAlert {
		return m.alertView(view)
	}
	return view
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
	uiPaneTitle("WORKSPACE / " + m.name)
	if m.showPreferences {
		uiPaneTitle("PREFERENCES")
	}
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
