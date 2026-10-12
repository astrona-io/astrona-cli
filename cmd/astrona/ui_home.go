package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/proctor"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type uiHomeRun struct {
	name, kind, status, runID string
	started                   time.Time
	openable                  bool
	activeTime                time.Duration
	activityTracked           bool
	activity                  string
	usage                     labstate.ResourceUsage
}

type uiHomeHistory struct {
	label, details string
	at             time.Time
}

type uiHomeSnapshot struct {
	runs    []uiHomeRun
	history []uiHomeHistory
	warning string
}

func readHomeContainers(ctx context.Context) (map[string][]kindContainer, error) {
	containers, _, err := readHomeEngine(ctx)
	return containers, err
}

func readHomeEngine(ctx context.Context) (map[string][]kindContainer, string, error) {
	providers := []string{"docker", "podman"}
	if provider := os.Getenv("KIND_EXPERIMENTAL_PROVIDER"); provider != "" {
		if provider != "docker" && provider != "podman" {
			return nil, "", fmt.Errorf("invalid KIND_EXPERIMENTAL_PROVIDER: %q", provider)
		}
		providers = []string{provider}
	}
	var failures []string
	for _, provider := range providers {
		path, err := exec.LookPath(provider)
		if err != nil {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		cmd := exec.CommandContext(readCtx, path, "ps", "-a", "--filter", "label=io.x-k8s.kind.cluster", "--format", `{{.Names}}|{{.State}}|{{.Label "io.x-k8s.kind.cluster"}}|{{.Label "io.x-k8s.kind.role"}}`)
		cmd.WaitDelay = time.Second
		var out workspaceOutput
		cmd.Stdout = &out
		err = cmd.Run()
		cancel()
		if err == nil {
			return groupKindContainers(string(out.Bytes())), path, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", provider, err))
	}
	return nil, "", fmt.Errorf("container engine unavailable; use astrona setup / doctor (%s)", strings.Join(failures, "; "))
}

// readUIHome performs bounded, read-only container discovery. History remains
// available without an installed or running engine, including an empty home.
func readUIHome(ctx context.Context) uiHomeSnapshot {
	var data uiHomeSnapshot
	warn := func(err error) { data.warning += err.Error() + "\n" }
	names, err := labstate.Names()
	if err != nil {
		warn(err)
	}
	containers := map[string][]kindContainer{}
	engineAvailable := false
	var usageSamples map[string]uiContainerUsage
	if len(names) > 0 {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		live, provider, err := readHomeEngine(readCtx)
		if err == nil {
			usageSamples = readUIContainerUsage(readCtx, provider, live)
		}
		cancel()
		if err != nil {
			warn(err)
		} else {
			engineAvailable, containers = true, live
		}
	}
	owners := map[string]string{}
	for name := range containers {
		links, _ := cluster.ReadLinks(name)
		for _, link := range links {
			owners[link.Cluster] = name
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, apphome.TestClusterPrefix()) || owners[name] != "" {
			continue
		}
		st, err := labstate.Load(name)
		if err != nil {
			warn(err)
			continue
		}
		if st == nil || st.Source == nil { // excludes remembered linked clusters
			continue
		}
		run := uiHomeRun{name: name, kind: "Lab", status: "Unavailable", runID: workspaceRunID(st)}
		run.started, _ = time.Parse(time.RFC3339, st.StartedAt)
		run.activeTime, run.activityTracked, err = labstate.ActiveTime(name, st.StartedAt)
		if err != nil {
			warn(err)
		}
		if st.Session.IsPlayground() || isPlaygroundName(st.Source.Catalog) {
			run.kind = "Playground"
		}
		if cs := containers[name]; len(cs) > 0 {
			run.status = containerStatus(cs)
			run.openable = run.status == "Running" || strings.HasPrefix(run.status, "Degraded")
		} else if engineAvailable {
			run.status = "Not found / non-Kind run"
		}
		run.activity = "—"
		if run.openable {
			run.activity = "Paused"
			active, err := labstate.HasActiveUI(name, st.StartedAt, time.Now())
			if err != nil {
				warn(err)
			} else if active {
				run.activity = "Active"
			}
		}
		run.usage = uiRunUsage(name, st.StartedAt, containers, usageSamples)
		data.runs = append(data.runs, run)
	}
	labs, err := proctor.HistoryLabs()
	if err != nil {
		warn(err)
	}
	for _, lab := range labs {
		if strings.HasPrefix(lab, apphome.TestClusterPrefix()) {
			continue
		}
		attempts, err := proctor.LoadAttempts(lab)
		if err != nil {
			warn(err)
			continue
		}
		start := max(0, len(attempts)-100)
		for i := start; i < len(attempts); i++ {
			a := attempts[i]
			result := "FAIL"
			if a.Pass {
				result = "PASS"
			}
			label := fmt.Sprintf("%s  %s  %d/%d  attempt #%d", lab, result, a.Earned, a.Max, i+1)
			details := label + "\nSubmitted: " + a.Time.Local().Format(time.RFC1123) + "\n"
			for _, check := range a.Checks {
				verdict := "FAIL"
				if check.Pass {
					verdict = "PASS"
				}
				details += fmt.Sprintf("\n[%s] %s", verdict, check.Name)
			}
			if a.OverTime {
				details += "\nSubmitted after the exam time limit."
			}
			data.history = append(data.history, uiHomeHistory{label: label, details: details, at: a.Time})
		}
	}
	runs, err := labstate.FinishedRuns()
	if err != nil {
		warn(err)
	}
	for _, run := range runs {
		label := fmt.Sprintf("%s  %s finished", run.Lab, run.Kind)
		details := fmt.Sprintf("%s\n%s\nStarted: %s\nFinished: %s\nElapsed: %s", label, run.Catalog, run.StartedAt.Local().Format(time.RFC1123), run.FinishedAt.Local().Format(time.RFC1123), max(time.Duration(0), run.FinishedAt.Sub(run.StartedAt)).Round(time.Second))
		if spent, tracked, err := labstate.ActiveTime(run.Lab, run.StartedAt.UTC().Format(time.RFC3339)); err != nil {
			warn(err)
		} else if tracked {
			details += "\nTime spent (excluding idle): " + formatUptime(spent)
		}
		data.history = append(data.history, uiHomeHistory{label: label, details: details, at: run.FinishedAt})
	}
	slices.SortFunc(data.history, func(a, b uiHomeHistory) int { return b.at.Compare(a.at) })
	data.history = data.history[:min(100, len(data.history))]
	return data
}

type uiHomeModel struct {
	deleting                     *uiHomeRun
	deleteYes, destroy           bool
	data                         uiHomeSnapshot
	notice                       string
	tab, selected, width, height int
	detail, quit, open, polling  bool
	scroll                       int
	nextRefresh                  time.Time
	refresh                      func() tea.Msg
	editor                       workspaceModel
	noticeError                  bool
}

func (m uiHomeModel) Init() tea.Cmd { return tea.Batch(workspaceTickCmd(), m.refresh) }

func (m uiHomeModel) history() []uiHomeHistory {
	if m.notice == "" {
		return m.data.history
	}
	return append([]uiHomeHistory{{label: "Latest action", details: m.notice}}, m.data.history...)
}

func (m uiHomeModel) count() int {
	if m.tab == 0 {
		return len(m.data.runs)
	}
	if m.tab == 1 {
		return len(m.history())
	}
	return 0
}

func (m uiHomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.editor.width, m.editor.height = msg.Width, msg.Height
	case uiHomeSnapshot:
		// Keep the selected run stable if a refresh inserts/removes rows.
		selectedName := ""
		selectedHistory := uiHomeHistory{}
		if m.tab == 0 && m.selected < len(m.data.runs) {
			selectedName = m.data.runs[m.selected].name
		}
		if m.tab == 1 && m.selected < len(m.history()) {
			selectedHistory = m.history()[m.selected]
		}
		m.data, m.polling = msg, false
		for i, run := range msg.runs {
			if run.name == selectedName {
				m.selected = i
			}
		}
		if m.tab == 1 {
			for i, item := range m.history() {
				if item.label == selectedHistory.label && item.at.Equal(selectedHistory.at) {
					m.selected = i
				}
			}
		}
		m.selected = min(m.selected, max(0, m.count()-1))
		m.nextRefresh = time.Now().Add(5 * time.Second)
	case workspaceTick:
		if !m.polling && m.refresh != nil && !time.Time(msg).Before(m.nextRefresh) {
			m.polling = true
			return m, tea.Batch(workspaceTickCmd(), m.refresh)
		}
		return m, workspaceTickCmd()
	case tea.KeyMsg:
		if m.deleting != nil {
			if !m.deleteDialogFits() {
				if msg.String() == "esc" || msg.String() == "ctrl+c" {
					m.deleting = nil
				}
				return m, nil
			}
			if msg.Paste {
				return m, nil
			}
			switch msg.String() {
			case "esc", "ctrl+c", "n", "N":
				m.deleting = nil
				m.deleteYes = false
			case "tab", "left", "right":
				m.deleteYes = !m.deleteYes
			case "y", "Y":
				m.deleteYes = true
			case "enter":
				if m.deleteYes {
					m.destroy = true
					return m, tea.Quit
				}
				m.deleting = nil
			}
			return m, nil
		}
		m.editor.home, m.editor.width, m.editor.height = true, m.width, m.height
		if m.editor.showPreferences || m.editor.showAlert {
			next, command := m.editor.Update(msg)
			m.editor = next.(workspaceModel)
			return m, command
		}
		switch msg.String() {
		case "p":
			m.editor.action = workspaceLoadPreferences
			return m, tea.Quit
		case "f1", "!":
			message := m.data.warning
			if m.noticeError {
				message = m.notice + "\n" + message
			}
			if strings.TrimSpace(message) != "" {
				m.editor.notice, m.editor.showAlert, m.editor.alertScroll = message, true, 0
			}
		case "ctrl+d":
			if m.width >= 48 && m.height >= 14 && m.tab == 0 && m.selected < len(m.data.runs) {
				run := m.data.runs[m.selected]
				m.deleting = &run
				m.deleteYes = false
			}
		case "q", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "esc":
			m.detail, m.scroll = false, 0
		case "tab":
			m.tab = (m.tab + 1) % 3
			m.selected, m.scroll, m.detail = 0, 0, false
		case "1", "2", "3":
			m.tab = int(msg.String()[0] - '1')
			m.selected, m.scroll, m.detail = 0, 0, false
		case "?":
			m.tab, m.detail, m.scroll = 2, false, 0
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "down", "j":
			m.selected = min(max(0, m.count()-1), m.selected+1)
		case "pgdown":
			m.scroll += max(1, m.height/2)
		case "pgup":
			m.scroll = max(0, m.scroll-max(1, m.height/2))
		case "r":
			if !m.polling && m.refresh != nil {
				m.polling = true
				return m, m.refresh
			}
		case "enter":
			if m.tab == 0 && m.selected < len(m.data.runs) {
				if m.data.runs[m.selected].openable {
					m.open = true
					return m, tea.Quit
				}
				m.notice = "This saved run cannot be opened now. Check the engine, or use astrona start / doctor for a stopped or missing run."
				m.noticeError = true
			} else if m.tab == 1 && m.count() > 0 {
				m.detail, m.scroll = true, 0
			}
		}
	}
	return m, nil
}

func (m uiHomeModel) View() string {
	editor := m.editor
	editor.home, editor.width, editor.height = true, m.width, m.height
	if m.width < 48 || m.height < 14 || editor.showPreferences {
		return editor.View()
	}
	w, h := m.width, editor.bodyHeight()
	message := m.notice
	isError := m.noticeError
	if m.data.warning != "" {
		message, isError = strings.TrimSpace(m.data.warning), true
	}
	if m.tab == 0 && len(m.data.runs) > 0 {
		h -= 2
	}
	if message != "" {
		h -= 4
	}
	tabs := []string{"Active runs", "History", "Help", "Preferences"}
	keys := []string{"<1>", "<2>", "<3>", "<p>"}
	if w < 72 {
		tabs = []string{"Runs", "History", "Help", "Settings"}
		keys = []string{"1", "2", "3", "p"}
	}
	editor.navigation = workspaceNav(tabs, keys, m.tab)
	title, content := "ACTIVE RUNS", ""
	if m.tab == 2 {
		title = "HELP"
		content = "1 Active runs · 2 History · 3 Help · p Preferences\nSelect a running Kind lab and press Enter for its task and terminal tabs.\nCtrl+D opens a force-delete confirmation for the selected run.\nCtrl+C returns from a lab to Home and pauses time spent.\nCtrl+T names a new terminal; mouse clicks or Ctrl+N/P change tabs. F3 opens lab details.\nHistory includes submission attempts and completed labs/playgrounds.\nOnly the latest 100 persistent entries are shown; astrona progress summarizes all graded attempts.\nF1 opens alert details. Esc closes dialogs.\nLogin status uses saved credentials; it does not check the API.\nUse astrona login to sign in, or astrona whoami to check an expired login.\nLocal profile: ~/" + apphome.Name() + "\n" + m.data.warning
		content = workspacePage(content, w-4, h-2, m.scroll)
	} else if m.tab == 1 && m.detail && m.count() > 0 {
		title = "HISTORY / DETAILS"
		content = workspacePage(m.history()[m.selected].details, w-4, h-2, m.scroll)
	} else {
		if m.tab == 1 {
			title = "HISTORY"
		}
		title += fmt.Sprintf(" [%d]", m.count())
		if m.count() == 0 {
			content = "No active runs.\n\nStart a lab with astrona run <lab> --ui.\nPress p for preferences."
			if m.tab == 1 {
				content = "No history yet.\nSubmit or finish a lab to see it here."
			}
			content = lipgloss.Place(w-4, h-2, lipgloss.Center, lipgloss.Center, content)
		} else {
			var rows []string
			page := max(1, h-2)
			if m.tab == 0 {
				rows = append(rows, workspaceMuted.Bold(true).Render("  "+uiRunActivityColumns("LAB", "TYPE", "STATUS", "STATE", "ACTIVE", w-6)))
				page = max(1, page-1)
			}
			start := max(0, m.selected-page+1)
			for i := start; i < min(m.count(), start+page); i++ {
				label := ""
				if m.tab == 0 {
					r := m.data.runs[i]
					age := "—"
					if r.activityTracked {
						age = formatUptime(r.activeTime)
					}
					label = uiRunActivityColumns(r.name, r.kind, r.status, r.activity, age, w-6)
				} else {
					item := m.history()[i]
					label = item.label
					if !item.at.IsZero() {
						label += "  " + humanAgo(time.Since(item.at))
					}
				}
				if i == m.selected {
					rows = append(rows, workspaceSelected(label, w-4))
				} else {
					style := workspaceMuted
					if m.tab == 0 && m.data.runs[i].openable {
						style = workspaceFocus
					}
					rows = append(rows, style.Render("  "+sanitizeLine(label)))
				}
			}
			content = strings.Join(rows, "\n")
		}
	}
	body := workspacePanel(title, content, w, h, workspaceFocus)
	if m.tab == 0 && len(m.data.runs) > 0 {
		r := m.data.runs[min(m.selected, len(m.data.runs)-1)]
		body += "\n" + workspaceFit(uiUsageText(r.usage), w, 1) + "\n" + workspaceFit("Averages: observed samples · all lab nodes and linked clusters", w, 1)
	}
	footer := []string{"p preferences  tab sections  q quit", "↑/↓ select  Enter open  Ctrl+D delete  r refresh"}
	if m.detail || m.tab == 2 {
		footer[1] = "pgup/pgdn scroll  Esc back  tab sections"
	}
	if message != "" {
		if isError {
			body += "\n" + workspaceAlert(message, w)
		} else {
			body += "\n" + workspacePanel("STATUS", workspacePage(message, w-4, 2, 0), w, 4, workspaceMuted)
		}
	}
	running := 0
	for _, run := range m.data.runs {
		if run.openable {
			running++
		}
	}
	view := editor.frame("HOME", fmt.Sprintf("Running: %d  ·  Saved: %d  ·  History: %d  ·  Refresh: every 5s", running, len(m.data.runs), len(m.data.history)), body, footer)
	if m.deleting != nil {
		if !m.deleteDialogFits() {
			return workspaceDialog(view, "FORCE DELETE", "Resize the terminal to read the full warning.\nEsc cancels.", m.width, m.height, true)
		}
		return workspaceDialog(view, "FORCE DELETE", uiDeleteMessage(m.deleting.name, m.deleteYes), m.width, m.height, true)
	}
	if editor.showAlert {
		return editor.alertView(view)
	}
	return view
}

func runUIHomeScreen(ctx context.Context, m uiHomeModel) (uiHomeModel, error) {
	uiPaneTitle("HOME")
	if m.editor.showPreferences {
		uiPaneTitle("PREFERENCES")
	}
	screenCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.refresh = func() tea.Msg { return readUIHome(screenCtx) }
	m.polling, m.open, m.quit, m.destroy = true, false, false, false
	m.editor.action = workspaceNone
	final, err := tea.NewProgram(m, tea.WithContext(screenCtx), tea.WithAltScreen()).Run()
	if err != nil {
		return m, fmt.Errorf("UI home: %w", err)
	}
	return final.(uiHomeModel), nil
}

type uiHomeDriver struct {
	destroy        func(uiHomeRun) error
	screen         func(context.Context, uiHomeModel) (uiHomeModel, error)
	open           func(uiHomeRun) (string, error)
	preferences    func() (workspacePreferences, error)
	savePreference func(workspaceSetting, string) error
}

func driveUIHome(ctx context.Context, m uiHomeModel, d uiHomeDriver) error {
	for {
		var err error
		m, err = d.screen(ctx, m)
		if err != nil {
			return err
		}
		if m.destroy && m.deleting != nil {
			err = d.destroy(*m.deleting)
			m.notice, m.noticeError = "Deleted "+m.deleting.name+". All work in this run was discarded.", err != nil
			if err != nil {
				m.notice = err.Error()
			}
			m.deleting, m.destroy, m.deleteYes = nil, false, false
			m.tab, m.selected, m.detail = 0, 0, false
			continue
		}
		switch m.editor.action {
		case workspaceLoadPreferences, workspaceSavePreference:
			applyWorkspacePreference(&m.editor, d.preferences, d.savePreference)
			m.editor.action = workspaceNone
			continue
		}
		if m.quit || !m.open {
			return nil
		}
		if m.selected >= len(m.data.runs) || !m.data.runs[m.selected].openable {
			m.notice = "That run is no longer available."
			m.noticeError = true
			continue
		}
		m.notice, err = d.open(m.data.runs[m.selected])
		m.noticeError = err != nil
		if err != nil {
			m.notice = err.Error()
		}
		m.tab, m.selected, m.scroll, m.detail = 0, 0, 0, false
	}
}

func runUIHome(ctx context.Context, flags *rootFlags, notice string) error {
	ctx, closeTerminals := uiTerminalContext(ctx)
	defer closeTerminals()
	return driveUIHome(ctx, uiHomeModel{notice: notice}, uiHomeDriver{
		screen:  runUIHomeScreen,
		destroy: func(run uiHomeRun) error { return forceDeleteUIRun(ctx, run, flags.verbose) },
		preferences: func() (workspacePreferences, error) {
			path, err := settingsPath(flags)
			if err != nil {
				return workspacePreferences{}, err
			}
			return loadWorkspacePreferences(path)
		},
		savePreference: func(row workspaceSetting, value string) error {
			path, err := settingsPath(flags)
			if err != nil {
				return err
			}
			return saveWorkspacePreference(ctx, path, row, value)
		},
		open: func(run uiHomeRun) (string, error) {
			if err := workspaceGuard(run.name, run.runID); err != nil {
				return "", err
			}
			st, err := labstate.Load(run.name)
			if err != nil {
				return "", err
			}
			if st == nil || st.Source == nil {
				return "", fmt.Errorf("run %s has no saved source", run.name)
			}
			localFlags := *flags
			applySource(&localFlags, st.Source)
			cfg, baseDir, cleanup, err := LoadLabForCommand(&localFlags)
			if err != nil {
				return "", err
			}
			defer cleanup()
			if config.NormalizeClusterName(cfg.Metadata.Name) != run.name {
				return "", fmt.Errorf("saved config now names a different lab; refusing to open %s", run.name)
			}
			return openLabWorkspace(ctx, cfg, baseDir, &localFlags, run.runID)
		},
	})
}
