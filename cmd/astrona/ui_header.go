package main

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"astrona/internal/account"
	"astrona/internal/apphome"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Keep only display fields in the UI. Reading the header must never renew
// credentials or create API traffic, including during a local-only lab run.
type uiAccountStatus struct {
	label, username, site string
	signedIn, warning     bool
}

func readUIAccountStatus() tea.Msg {
	status := uiAccountStatus{label: "Not logged in"}
	store, err := account.DefaultStore()
	if err != nil {
		status.label, status.warning = "Login unavailable", true
		return status
	}
	creds, loadErr := store.Load()
	savedSite := ""
	if creds != nil {
		savedSite = creds.Site
	}
	site, err := account.ResolveSite(os.Getenv, savedSite)
	if err != nil {
		status.label, status.warning = "Check site settings", true
		return status
	}
	if parsed, err := url.Parse(site); err == nil {
		status.site = parsed.Host
	}
	if loadErr != nil && !errors.Is(loadErr, account.ErrSignedOut) {
		status.label, status.warning = "Login unavailable", true
		return status
	}
	if creds == nil || creds.Site != site {
		return status
	}
	status.username = creds.Username
	if !time.Now().Before(creds.AccessExpiresAt) {
		status.label, status.warning = "Login needs checking", true
		return status
	}
	status.label, status.signedIn = "Logged in", true
	return status
}

func uiAccountTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return readUIAccountStatus() })
}

func uiHeaderHeight(width, height int) int {
	if width >= 100 && height >= 28 {
		return 7
	}
	if width >= 64 && height >= 22 {
		return 5
	}
	return 2
}

const uiWordmark = `   _   ___ _____ ___  ___  _  _   _
  /_\ / __|_   _| _ \/ _ \| \| | /_\
 / _ \\__ \ | | |   / (_) | .` + "`" + ` |/ _ \
/_/ \_\___/ |_| |_|_\\___/|_|\_/_/ \_\`

func (m *uiPaneModel) header() string {
	status := m.account
	if status.label == "" {
		status.label, status.warning = "Checking login…", true
	}
	style := workspaceDanger
	if status.signedIn {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	} else if status.warning {
		style = workspaceWarn
	}
	profile := "Release"
	if apphome.Development() {
		profile = "Development"
	}
	height := uiHeaderHeight(m.width, m.height)
	if height == 2 {
		return workspaceFit(style.Render("● "+status.label)+"  "+workspaceAccent.Render("ASTRONA")+"\n"+
			workspaceMuted.Render(profile+" · "+sanitizeLine(status.site)), m.width, height)
	}
	user := status.username
	if user == "" {
		user = "Guest"
	}
	field := func(label, value string) string {
		return workspaceAccent.Render(workspaceFit(label+":", 10, 1)) + sanitizeLine(value)
	}
	rows := []string{
		workspaceAccent.Render("Login:    ") + style.Render(status.label),
		field("User", user), field("Site", status.site), field("Profile", profile),
		field("Version", Version), field("View", uiPaneLabel(m.title)),
	}
	if height == 5 {
		rows = []string{rows[0], rows[1], rows[2], field("Profile", profile+" / "+Version)}
	}
	leftWidth := 32
	if m.width >= 120 {
		leftWidth = min(48, m.width/3)
	}
	blocks := []string{workspaceFit(strings.Join(rows, "\n"), leftWidth, height-1)}
	hints := m.headerShortcuts()
	remaining := m.width - leftWidth
	logoWidth := 39
	showLogo := height == 7 && remaining >= 88
	if showLogo {
		remaining -= logoWidth
	}
	columns := 1
	if remaining >= 48 {
		columns = 2
	}
	for col := range columns {
		var lines []string
		for i := col * (height - 1); i < min(len(hints), (col+1)*(height-1)); i++ {
			lines = append(lines, workspaceFocus.Render(workspaceFit(hints[i][0], 11, 1))+workspaceMuted.Render(hints[i][1]))
		}
		columnWidth := remaining / columns
		if col == columns-1 {
			columnWidth += remaining % columns
		}
		blocks = append(blocks, workspaceFit(strings.Join(lines, "\n"), columnWidth, height-1))
	}
	if showLogo {
		blocks = append(blocks, workspaceFit(workspaceAccent.Render(uiWordmark)+"\n"+workspaceMuted.Render("                 LAB WORKSPACE"), logoWidth, height-1))
	}
	return workspaceFit(lipgloss.JoinHorizontal(lipgloss.Top, blocks...), m.width, height)
}

func (m *uiPaneModel) headerShortcuts() [][2]string {
	if m.ended {
		return [][2]string{{"<enter>", "Close"}, {"<↑/↓>", "Scroll output"}}
	}
	if m.controls || m.closing {
		return [][2]string{{"<esc>", "Resume"}, {"<↑/↓>", "Scroll output"}, {"<pgup/dn>", "Scroll page"}, {"<q>", "Close UI"}}
	}
	section, _, _ := strings.Cut(m.title, " / ")
	switch section {
	case "HOME":
		return [][2]string{{"<1>", "Active runs"}, {"<2>", "History"}, {"<3>", "Help"}, {"<p>", "Preferences"}, {"<n>", "Start lab"}, {"<enter>", "Open selected"}, {"<↑/↓>", "Select run"}, {"<ctrl-d>", "Force delete"}, {"<r>", "Refresh"}, {"<tab>", "Next section"}, {"<q>", "Quit"}}
	case "TASK":
		return [][2]string{{"<ctrl-t>", "New terminal"}, {"<ctrl-n/p>", "Switch tab"}, {"<ctrl-c>", "Overview / pause"}, {"<alt-c>", "Interrupt command"}, {"<f2>", "Task / terminal"}, {"<f3>", "Lab details"}}
	case "WORKSPACE":
		return [][2]string{{"<enter>", "Task + terminals"}, {"<tab>", "Task / results"}, {"<s>", "Shell"}, {"<p>", "Preferences"}, {"<↑/↓>", "Select cluster"}, {"<ctrl-c>", "Overview / pause"}, {"<q>", "Exit menu"}, {"<pgup/dn>", "Scroll task"}, {"<f1>", "Alert details"}}
	case "SHELL":
		return [][2]string{{"exit", "Back to lab"}, {"<ctrl-c>", "Interrupt"}, {"<ctrl-]>", "Pane controls"}}
	case "PREFERENCES":
		return [][2]string{{"<↑/↓>", "Select setting"}, {"<enter>", "Edit / save"}, {"<esc>", "Back"}}
	default:
		return [][2]string{{"<ctrl-]>", "Pane controls"}, {"<ctrl-c>", "Interrupt"}}
	}
}

func uiPaneLabel(title string) string {
	section, name, _ := strings.Cut(title, " / ")
	labels := map[string]string{
		"STARTUP": "Starting Astrona", "HOME": "Home", "SETUP": "Setting up your lab",
		"TASK": "Task and terminals", "WORKSPACE": "Lab workspace", "SHELL": "Shell", "GRADING": "Grading",
		"DESTROY": "Removing lab", "START LAB": "Start a lab",
	}
	if label, ok := labels[section]; ok {
		section = label
	}
	if name != "" {
		section += " · " + name
	}
	return section
}

func workspaceNav(labels, keys []string, active int) string {
	items := make([]string, len(labels))
	for i, label := range labels {
		text := " " + keys[i] + " " + label + " "
		style := workspaceMuted
		if i == active {
			text = "[" + strings.TrimSpace(text) + "]"
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true)
		}
		items[i] = style.Render(text)
	}
	return strings.Join(items, " ")
}
