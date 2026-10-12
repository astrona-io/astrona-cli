package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type workspaceSetting struct {
	key, value string
	saved      bool
	definition reviewedSetting
}

type workspacePreferences struct {
	path, draft, notice string
	rows                []workspaceSetting
	selected, scroll    int
	editing, confirming bool
	noticeError         bool
}

func loadWorkspacePreferences(path string) (workspacePreferences, error) {
	p := workspacePreferences{path: path}
	saved, err := loadSettings(path)
	if err != nil {
		return p, err
	}
	defs := reviewDefinitions()
	for _, setting := range settingsDefinitions() {
		value, err := saved.value(setting.key)
		if err != nil {
			return p, err
		}
		_, explicit := saved[setting.key]
		p.rows = append(p.rows, workspaceSetting{setting.key, string(value), explicit, defs[setting.key]})
	}
	return p, nil
}

// Re-read under the CLI's settings lock. Preserve unrelated concurrent edits,
// and reject a stale choice instead of silently replacing another writer's value.
func saveWorkspacePreference(ctx context.Context, path string, row workspaceSetting, value string) error {
	parsed, err := parseSetting(row.key, value)
	if err != nil {
		return err
	}
	return withSettingsLock(path, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := loadSettings(path)
		if err != nil {
			return err
		}
		got, err := current.value(row.key)
		if err != nil {
			return err
		}
		_, explicit := current[row.key]
		if string(got) != row.value || explicit != row.saved {
			return fmt.Errorf("%s changed in another command; press Esc, then r to reload before editing", row.key)
		}
		current[row.key] = parsed
		// Editing a value is not approval of new definitions, terms or tracking.
		return writeSettingsFile(path, current)
	})
}

func (m workspaceModel) updatePreferences(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.preferences
	key := msg.String()
	if key == "esc" || key == "ctrl+c" || key == "ctrl+d" {
		if p.editing || p.confirming {
			p.editing, p.confirming, p.draft = false, false, ""
			p.notice = "Edit cancelled; nothing saved."
			p.noticeError = false
		} else {
			m.showPreferences = false
		}
		return m, nil
	}
	// Do not confirm writes when the terminal cannot display the editor.
	if m.width < 48 || m.height < 14 {
		return m, nil
	}
	if key == "pgdown" {
		p.scroll += 3
		return m, nil
	}
	if key == "pgup" {
		p.scroll = max(0, p.scroll-3)
		return m, nil
	}
	if p.confirming {
		switch key {
		case "y":
			m.action = workspaceSavePreference
			return m, tea.Quit
		case "n", "q":
			p.confirming, p.editing, p.draft = false, false, ""
			p.notice = "Change declined; current value kept."
			p.noticeError = false
		case "enter":
			p.confirming, p.editing = false, true
		}
		return m, nil
	}
	if p.editing {
		switch key {
		case "enter":
			row := p.rows[p.selected]
			value := strings.TrimSpace(p.draft)
			if value == "" && !row.definition.ExplicitChoice {
				p.editing = false
				p.notice = "No change; nothing saved."
				p.noticeError = false
				return m, nil
			}
			parsed, err := parseSetting(row.key, value)
			if err != nil {
				p.notice = err.Error()
				p.noticeError = true
				return m, nil
			}
			p.draft, p.editing, p.confirming = string(parsed), false, true
			p.notice, p.scroll = "", 0
			p.noticeError = false
		case "backspace", "ctrl+h":
			if len(p.draft) > 0 {
				p.draft = p.draft[:len(p.draft)-1]
			}
		case "ctrl+u":
			p.draft = ""
		default:
			if msg.Type == tea.KeyRunes {
				for _, r := range msg.Runes {
					if r >= ' ' && r <= '~' && len(p.draft) < 64 {
						p.draft += string(r)
					}
				}
			}
		}
		return m, nil
	}
	switch key {
	case "q", "p":
		m.showPreferences = false
	case "r":
		m.action = workspaceLoadPreferences
		return m, tea.Quit
	case "up", "k":
		p.selected, p.scroll = max(0, p.selected-1), 0
	case "down", "j":
		p.selected, p.scroll = min(max(0, len(p.rows)-1), p.selected+1), 0
	case "enter":
		if len(p.rows) > 0 {
			p.editing, p.draft, p.notice, p.scroll = true, "", "", 0
			p.noticeError = false
		}
	}
	return m, nil
}

func preferenceEffect(key string) string {
	if key == "cache-retention-days" || key == "cache-max-size-gb" {
		return "Cache policy is checked after the workspace closes. Saved labs keep their caches protected; saving here does not delete files."
	}
	return "Used by future CLI commands. This workspace keeps its current command options; the running lab is unchanged."
}

// Describe the requested value, not a recommendation to change it.
func preferenceChangeImpact(key, value string) string {
	switch key {
	case "verbose":
		if value == "true" {
			return "Show full command output to help troubleshoot failures; terminal output will be longer."
		}
		return "Use compact progress output; detailed command output will no longer stream by default."
	case "keep-context":
		if value == "true" {
			return "Keep your existing kubectl context so lab commands do not switch it. Use the lab's isolated kubeconfig to work on it."
		}
		return "Allow lab commands to switch your kubectl context to the lab. Subsequent kubectl commands may target that lab."
	case "parallel":
		return "Start up to " + value + " linked clusters at once. Higher concurrency can reduce startup time but uses more resources at once."
	case "login-reminder":
		if value == "true" {
			return "Offer login before each guest lab run so catalog progress can be tracked."
		}
		return "Allow guest lab runs without login reminders. You can still sign in with astrona login."
	case "update-check":
		if value == "true" {
			return "Allow release checks to show update notices. GitHub receives your IP address. CI, non-terminal sessions and development builds still skip checks."
		}
		return "Stop automatic release checks and update notices. You will need to check for new releases yourself."
	case "cache-retention-days":
		return "Allow cleanup of eligible disposable caches older than " + value + " days. Shorter retention can free disk space but require downloads again."
	case "cache-max-size-gb":
		if value == "0" {
			return "Remove the cache size limit; disk use can grow without a size cap. The age limit still applies."
		}
		return "Target a disposable-cache limit of " + value + " GB. Cleanup may remove eligible caches and require downloads again; protected lab caches can keep usage above the limit."
	}
	return "Use the selected value for this preference."
}

func (m workspaceModel) preferencesView() string {
	p := m.preferences
	w, h := m.width, m.bodyHeight()
	if p.notice != "" {
		h -= 4
	}
	footer := []string{"↑/↓ select   enter edit   r reload   Esc back", "pgup/pgdn scroll details"}
	context := p.path
	details := "Could not load settings. Press r to retry, or Esc to return."
	title := "DETAILS"
	if len(p.rows) > 0 {
		row := p.rows[p.selected]
		source := "default"
		if row.saved {
			source = "saved"
		}
		context = fmt.Sprintf("Current: %s (%s)  Default: %s", settingValueText(row.value), source, settingValueText(row.definition.Default))
		title = row.key
		details = row.definition.Description + "\n\n" + preferenceEffect(row.key)
		if p.confirming {
			title = "APPLY PREFERENCE"
			details = fmt.Sprintf("Change: %s -> %s\nDefault: %s\nWhy: You requested this preference; changing it is optional.\nEffect: %s\n\n%s", settingValueText(row.value), settingValueText(p.draft), settingValueText(row.definition.Default), preferenceChangeImpact(row.key, p.draft), preferenceEffect(row.key))
			if row.key == "update-check" || row.definition.ExplicitChoice {
				details += "\n\n" + row.definition.Description
			}
			footer = []string{"Apply this change? [y/N]   y yes   n no", "enter edit   Esc cancel   pgup/pgdn scroll"}
		} else if p.editing {
			title = "EDIT " + row.key
			if row.definition.Default == "true" || row.definition.Default == "false" {
				details = "Enter yes or no; blank keeps the current value.\n\n" + details
			}
			if row.definition.ExplicitChoice {
				details = "Type your choice explicitly; Enter alone cannot accept it.\n" + details
			}
			footer = []string{"New value: " + p.draft + "_", "enter review   Ctrl+u clear   Esc cancel"}
		}
	}
	listWidth := min(36, w/2)
	if w < 80 || p.editing || p.confirming {
		listWidth = w
	}
	var rows []string
	visible := max(1, h-3)
	start := max(0, p.selected-visible+1)
	rows = append(rows, workspaceMuted.Render("  SETTING                 VALUE"))
	for i := start; i < min(len(p.rows), start+visible); i++ {
		row := p.rows[i]
		text := fmt.Sprintf("%-22s %s", row.key, settingValueText(row.value))
		if i == p.selected {
			rows = append(rows, workspaceSelected(text, listWidth-4))
		} else {
			rows = append(rows, "  "+sanitizeLine(text))
		}
	}
	body := workspacePanel("PREFERENCES", strings.Join(rows, "\n"), listWidth, h, workspaceFocus)
	if p.editing || p.confirming {
		dialogWidth := min(76, w-6)
		page := workspacePage(details, dialogWidth-4, h-2, p.scroll)
		body = workspaceDialog(body, title, page, w, h, false)
	} else if w >= 80 {
		rightWidth := w - listWidth
		right := workspacePanel(title, workspacePage(details, rightWidth-4, h-2, p.scroll), rightWidth, h, workspaceBorder)
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, right)
	} else {
		body = workspacePanel(title, workspacePage(details, w-4, h-2, p.scroll), w, h, workspaceFocus)
	}
	if p.notice != "" {
		if p.noticeError {
			body += "\n" + workspaceAlert(p.notice, w)
		} else {
			body += "\n" + workspacePanel("STATUS", workspacePage(p.notice, w-4, 2, 0), w, 4, workspaceMuted)
		}
	}
	return m.frame("PREFERENCES", context, body, footer)
}
