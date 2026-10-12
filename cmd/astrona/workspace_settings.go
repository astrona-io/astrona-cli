package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
		case "n", "enter", "q":
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
				return m, nil
			}
			parsed, err := parseSetting(row.key, value)
			if err != nil {
				p.notice = err.Error()
				return m, nil
			}
			p.draft, p.editing, p.confirming = string(parsed), false, true
			p.notice = ""
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

func (m workspaceModel) preferencesView() string {
	p := m.preferences
	w := max(1, m.width-2)
	clip := func(s string) string { return ansi.Truncate(sanitizeLine(s), w, "…") }
	if m.width < 48 || m.height < 14 {
		return "ASTRONA / PREFERENCES\nResize to at least 48 × 14.\nEsc cancels an edit or returns to the lab."
	}
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
	lines := []string{accent.Render("ASTRONA / PREFERENCES"), clip(p.path), ""}
	footer := []string{"↑/↓ select   enter edit   r reload   Esc back", "pgup/pgdn scroll details"}
	details := "Could not load settings. Press r to retry, or Esc to return to the lab."
	if len(p.rows) > 0 {
		row := p.rows[p.selected]
		if p.editing || p.confirming {
			lines = append(lines, accent.Render(clip(row.key)))
			if p.confirming {
				footer = []string{clip("Save " + row.key + " = " + p.draft + "?"), "y save   enter edit   Esc cancel"}
			} else {
				footer = []string{clip("New value: " + p.draft + "_"), "enter review   Ctrl+u clear   Esc cancel"}
			}
		} else {
			visible := min(len(p.rows), max(1, m.height-17))
			start := max(0, p.selected-visible+1)
			for i := start; i < min(len(p.rows), start+visible); i++ {
				prefix := "  "
				if i == p.selected {
					prefix = "> "
				}
				lines = append(lines, clip(prefix+p.rows[i].key+" = "+p.rows[i].value))
			}
		}
		source := "default"
		if row.saved {
			source = "saved"
		}
		details = fmt.Sprintf("Current: %s (%s)   Default: %s\n%s\n\n%s", row.value, source, row.definition.Default, row.definition.Description, preferenceEffect(row.key))
		if row.definition.ExplicitChoice && p.editing {
			details = "Type your choice explicitly; Enter alone cannot accept it.\n" + details
		}
	}
	if p.notice != "" {
		// Errors and save status stay visible even when the details are scrolled.
		lines = append(lines, clip(p.notice))
		// Keep truncated messages available in full in the scrollable details.
		if ansi.StringWidth(sanitizeLine(p.notice)) > w {
			details += "\n\n" + p.notice
		}
	}
	lines = append(lines, "")
	wrapped := strings.Split(ansi.Wrap(sanitizeTerminalText(details), w, ""), "\n")
	available := max(1, m.height-len(lines)-len(footer)-2)
	start := min(p.scroll, max(0, len(wrapped)-available))
	lines = append(lines, wrapped[start:min(len(wrapped), start+available)]...)
	if len(wrapped) > available {
		footer[0] = clip(footer[0])
		lines = append(lines, clip("Details: pgup/pgdn to scroll"))
	}
	return strings.Join(append(append(lines, ""), footer...), "\n")
}
