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
	"github.com/charmbracelet/x/ansi"
)

type uiHomeRun struct {
	name, kind, status, runID string
	started                   time.Time
	openable                  bool
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
	providers := []string{"docker", "podman"}
	if provider := os.Getenv("KIND_EXPERIMENTAL_PROVIDER"); provider != "" {
		if provider != "docker" && provider != "podman" {
			return nil, fmt.Errorf("invalid KIND_EXPERIMENTAL_PROVIDER: %q", provider)
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
			return groupKindContainers(string(out.Bytes())), nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", provider, err))
	}
	return nil, fmt.Errorf("container engine unavailable; use astrona setup / doctor (%s)", strings.Join(failures, "; "))
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
	if len(names) > 0 {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		live, err := readHomeContainers(readCtx)
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
		if st.Session.IsPlayground() || isPlaygroundName(st.Source.Catalog) {
			run.kind = "Playground"
		}
		if cs := containers[name]; len(cs) > 0 {
			run.status = containerStatus(cs)
			run.openable = run.status == "Running" || strings.HasPrefix(run.status, "Degraded")
		} else if engineAvailable {
			run.status = "Not found / non-Kind run"
		}
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
		data.history = append(data.history, uiHomeHistory{label: label, details: details, at: run.FinishedAt})
	}
	slices.SortFunc(data.history, func(a, b uiHomeHistory) int { return b.at.Compare(a.at) })
	data.history = data.history[:min(100, len(data.history))]
	return data
}

type uiHomeModel struct {
	data                         uiHomeSnapshot
	notice                       string
	tab, selected, width, height int
	detail, quit, open, polling  bool
	scroll                       int
	nextRefresh                  time.Time
	refresh                      func() tea.Msg
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
		switch msg.String() {
		case "q", "ctrl+c", "ctrl+d":
			m.quit = true
			return m, tea.Quit
		case "esc":
			m.detail, m.scroll = false, 0
		case "tab":
			m.tab = (m.tab + 1) % 3
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
			} else if m.tab == 1 && m.count() > 0 {
				m.detail, m.scroll = true, 0
			}
		}
	}
	return m, nil
}

func (m uiHomeModel) View() string {
	w, h := max(1, m.width), max(3, m.height)
	clip := func(s string) string { return ansi.Truncate(sanitizeLine(s), w, "…") }
	tabs := []string{"Active runs", "History", "Help"}
	tabs[m.tab] = "[" + tabs[m.tab] + "]"
	lines := []string{clip("ASTRONA / HOME"), clip(strings.Join(tabs, "  ")), clip(fmt.Sprintf("%d saved runs  •  %d recent history entries", len(m.data.runs), len(m.data.history)))}
	body := []string{}
	if m.notice != "" {
		body = append(body, clip(strings.Split(m.notice, "\n")[0]), "")
	}
	if m.data.warning != "" {
		body = append(body, clip("Some information is unavailable; see Help for details."))
	}
	if m.tab == 2 {
		text := "Start a lab: astrona run <lab> --ui\nOpen a saved run: select it in Active runs and press Enter.\nSubmit from the workspace with g, then return home; the lab stays running.\nDestroy from the workspace exit menu, with explicit confirmation.\nHistory includes submission attempts and completed labs/playgrounds.\nOnly the latest 100 persistent history entries are shown here; astrona progress summarizes all graded attempts.\nNo actions run when you leave Home.\nLocal profile: ~/" + apphome.Name() + "\n" + m.data.warning
		body = append(body, strings.Split(ansi.Wrap(sanitizeTerminalText(text), w, ""), "\n")...)
	} else if m.tab == 1 && m.detail && m.count() > 0 {
		body = append(body, strings.Split(ansi.Wrap(sanitizeTerminalText(m.history()[m.selected].details), w, ""), "\n")...)
	} else {
		if m.count() == 0 {
			if m.tab == 0 {
				body = append(body, "No active runs. Start one with astrona run <lab> --ui.")
			} else {
				body = append(body, "No history yet. Submit or finish a lab/playground to see it here.")
			}
		}
		page := max(1, h-7)
		start := max(0, m.selected-page+1)
		for i := start; i < min(m.count(), start+page); i++ {
			prefix := "  "
			if i == m.selected {
				prefix = "> "
			}
			label := ""
			if m.tab == 0 {
				r := m.data.runs[i]
				label = fmt.Sprintf("%s  %s  %s", r.name, r.kind, r.status)
				if !r.started.IsZero() {
					label += "  " + max(time.Duration(0), time.Since(r.started)).Round(time.Second).String()
				}
			} else {
				item := m.history()[i]
				label = item.label
				if !item.at.IsZero() {
					label += "  " + humanAgo(time.Since(item.at))
				}
			}
			body = append(body, clip(prefix+label))
		}
	}
	space := max(0, h-len(lines)-2)
	offset := 0
	if m.detail || m.tab == 2 {
		offset = min(m.scroll, max(0, len(body)-space))
	}
	for _, line := range body[offset:min(len(body), offset+space)] {
		lines = append(lines, clip(line))
	}
	lines = append(lines, "", clip("q quit  tab sections  ↑/↓ select  enter open/details  esc back  r refresh  ? help"))
	return strings.Join(lines[:min(h, len(lines))], "\n")
}

func runUIHomeScreen(ctx context.Context, m uiHomeModel) (uiHomeModel, error) {
	screenCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.refresh = func() tea.Msg { return readUIHome(screenCtx) }
	m.polling, m.open, m.quit = true, false, false
	final, err := tea.NewProgram(m, tea.WithContext(screenCtx), tea.WithAltScreen()).Run()
	if err != nil {
		return m, fmt.Errorf("UI home: %w", err)
	}
	return final.(uiHomeModel), nil
}

type uiHomeDriver struct {
	screen func(context.Context, uiHomeModel) (uiHomeModel, error)
	open   func(uiHomeRun) (string, error)
}

func driveUIHome(ctx context.Context, m uiHomeModel, d uiHomeDriver) error {
	for {
		var err error
		m, err = d.screen(ctx, m)
		if err != nil {
			return err
		}
		if m.quit || !m.open {
			return nil
		}
		if m.selected >= len(m.data.runs) || !m.data.runs[m.selected].openable {
			m.notice = "That run is no longer available."
			continue
		}
		m.notice, err = d.open(m.data.runs[m.selected])
		if err != nil {
			m.notice = err.Error()
		}
		m.tab, m.selected, m.scroll, m.detail = 0, 0, 0, false
	}
}

func runUIHome(ctx context.Context, flags *rootFlags, notice string) error {
	return driveUIHome(ctx, uiHomeModel{notice: notice}, uiHomeDriver{
		screen: runUIHomeScreen,
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
