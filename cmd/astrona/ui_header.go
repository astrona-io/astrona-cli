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
	"github.com/charmbracelet/x/ansi"
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
	label := "● " + status.label
	if status.username != "" && m.width >= 80 {
		label += " · " + ansi.Truncate(sanitizeLine(status.username), 24, "…")
	}
	brand := workspaceAccent.Render(" ASTRONA")
	if m.width >= 80 {
		brand += workspaceMuted.Render(" " + sanitizeLine(Version))
	}
	gap := m.width - ansi.StringWidth(brand) - ansi.StringWidth(label) - 1
	line := style.Render(label)
	if gap > 0 {
		line = brand + strings.Repeat(" ", gap) + line
	}
	profile := "Release"
	if apphome.Development() {
		profile = "Development"
	}
	details := " Profile: " + profile + "  ·  Site: " + sanitizeLine(status.site)
	if status.site == "" {
		details = " Profile: " + profile
	}
	return workspaceFit(line+"\n"+workspaceMuted.Render(details), m.width, 2)
}

func uiPaneLabel(title string) string {
	section, name, _ := strings.Cut(title, " / ")
	labels := map[string]string{
		"STARTUP": "Starting Astrona", "HOME": "Home", "SETUP": "Setting up your lab",
		"WORKSPACE": "Lab workspace", "SHELL": "Shell", "GRADING": "Grading",
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
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true)
		}
		items[i] = style.Render(text)
	}
	return strings.Join(items, " ")
}
