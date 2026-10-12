package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
)

func preferencesFixture(t *testing.T, key string) workspaceModel {
	t.Helper()
	p, err := loadWorkspacePreferences(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range p.rows {
		if row.key == key {
			p.selected = i
		}
	}
	return workspaceModel{width: 80, height: 24, showPreferences: true, preferences: p}
}

func preferenceKey(m workspaceModel, key tea.KeyType) workspaceModel {
	result, _ := m.Update(tea.KeyMsg{Type: key})
	return result.(workspaceModel)
}

func TestWorkspacePreferenceEditing(t *testing.T) {
	for _, tc := range []struct {
		name, key                 string
		keys                      []string
		want                      workspaceAction
		editing, confirm, visible bool
	}{
		{"edit", "parallel", []string{"enter"}, workspaceNone, true, false, true},
		{"unchanged blank", "parallel", []string{"enter", "enter"}, workspaceNone, false, false, true},
		{"invalid", "parallel", []string{"enter", "0", "enter"}, workspaceNone, true, false, true},
		{"confirm", "parallel", []string{"enter", "2", "enter"}, workspaceNone, false, true, true},
		{"save", "parallel", []string{"enter", "2", "enter", "y"}, workspaceSavePreference, false, true, true},
		{"return to edit", "parallel", []string{"enter", "2", "enter", "enter"}, workspaceNone, true, false, true},
		{"cancel edit", "parallel", []string{"enter", "2", "esc"}, workspaceNone, false, false, true},
		{"cancel confirmation", "parallel", []string{"enter", "2", "enter", "ctrl+c"}, workspaceNone, false, false, true},
		{"leave", "parallel", []string{"esc"}, workspaceNone, false, false, false},
		{"reload", "parallel", []string{"r"}, workspaceLoadPreferences, false, false, true},
		{"sensitive blank", "update-check", []string{"enter", "enter"}, workspaceNone, true, false, true},
		{"sensitive false", "update-check", []string{"enter", "false", "enter", "y"}, workspaceSavePreference, false, true, true},
		{"lifecycle keys are input", "parallel", []string{"enter", "s", "g", "q", "d", "y"}, workspaceNone, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := preferencesFixture(t, tc.key)
			for _, key := range tc.keys {
				m = workspaceKey(m, key)
			}
			if m.action != tc.want || m.preferences.editing != tc.editing || m.preferences.confirming != tc.confirm || m.showPreferences != tc.visible {
				t.Fatalf("unexpected state: %+v", m)
			}
			if _, err := os.Stat(m.preferences.path); !os.IsNotExist(err) {
				t.Fatalf("rendering or input wrote settings: %v", err)
			}
		})
	}
	m := preferencesFixture(t, "parallel")
	m = workspaceKey(workspaceKey(m, "enter"), "123")
	m = preferenceKey(m, tea.KeyBackspace)
	if m.preferences.draft != "12" {
		t.Fatal(m.preferences.draft)
	}
	m = preferenceKey(m, tea.KeyCtrlU)
	if m.preferences.draft != "" {
		t.Fatal("clear failed")
	}
	m = workspaceKey(m, strings.Repeat("a", 100))
	if len(m.preferences.draft) != 64 {
		t.Fatal("unbounded input")
	}
	m.width = 20
	m.preferences.confirming = true
	m = workspaceKey(m, "y")
	if m.action != workspaceNone {
		t.Fatal("saved from unreadable terminal")
	}
}

func TestWorkspacePreferenceView(t *testing.T) {
	m := preferencesFixture(t, "update-check")
	for _, mode := range []string{"list", "edit", "confirm"} {
		t.Run(mode, func(t *testing.T) {
			m.preferences.editing, m.preferences.confirming = mode == "edit", mode == "confirm"
			m.preferences.draft = "false"
			for _, size := range [][2]int{{48, 14}, {80, 24}, {120, 40}} {
				m.width, m.height = size[0], size[1]
				view := ansi.Strip(m.View())
				if len(strings.Split(view, "\n")) > m.height {
					t.Fatalf("view exceeds height %d:\n%s", m.height, view)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > m.width {
						t.Fatalf("view exceeds width: %q", line)
					}
				}
				if !strings.Contains(view, "Default:") || !strings.Contains(view, "PREFERENCES") {
					t.Fatal(view)
				}
			}
			if !strings.Contains(strings.Join(strings.Fields(ansi.Strip(m.View())), " "), "GitHub receives your IP address") {
				t.Fatal("missing privacy explanation")
			}
		})
	}
	m.preferences.notice = "error\x1b[2J\x07"
	m.preferences.path += "\x1b[2J"
	if got := ansi.Strip(m.View()); strings.ContainsAny(got, "\x1b\x07") {
		t.Fatal("unsafe terminal text")
	}
	m = workspaceKey(m, "esc")
	m = workspaceKey(m, "pgdown")
	if m.preferences.scroll != 3 {
		t.Fatal("missing detail scrolling")
	}
	m = workspaceKey(m, "pgup")
	if m.preferences.scroll != 0 {
		t.Fatal("missing reverse scrolling")
	}
}

func TestWorkspacePreferenceSelection(t *testing.T) {
	m := preferencesFixture(t, "verbose")
	m.selected = 1
	for range 20 {
		m = workspaceKey(m, "down")
	}
	if m.preferences.selected != len(m.preferences.rows)-1 || m.selected != 1 {
		t.Fatal("preferences changed cluster selection or exceeded settings list")
	}
	for range 20 {
		m = workspaceKey(m, "up")
	}
	if m.preferences.selected != 0 || m.selected != 1 {
		t.Fatal("preferences changed cluster selection or exceeded settings list")
	}
	// A background lab refresh must not discard an in-progress preference edit.
	m = workspaceKey(workspaceKey(m, "enter"), "true")
	next, _ := m.Update(workspaceSnapshot{blocked: "Lab expired"})
	m = next.(workspaceModel)
	if !m.preferences.editing || m.preferences.draft != "true" || !m.showPreferences {
		t.Fatal("lab refresh discarded preference edit")
	}
}

// Run manually in a terminal; the settings file belongs to a temporary directory.
func TestWorkspacePreferenceTerminalSmoke(t *testing.T) {
	if os.Getenv("ASTRONA_WORKSPACE_SMOKE") != "1" || !isatty.IsTerminal(os.Stdin.Fd()) {
		t.Skip("manual terminal smoke test")
	}
	m := preferencesFixture(t, "verbose")
	path := m.preferences.path
	m.showPreferences = false
	m.name = "astro-preferences-test"
	m.task = "Press p. Save parallel = 2. Start editing update-check, then cancel with Esc. Return to the lab with Esc, then q and k to leave."
	snap := workspaceSnapshot{kind: "Lab", tracking: "Temporary settings fixture", clusters: []workspaceCluster{{name: m.name, status: "Fixture only"}}}
	m.snapshot = snap
	err := driveWorkspace(t.Context(), m, workspaceDriver{
		screen:      runWorkspaceScreen,
		refresh:     func(context.Context) workspaceSnapshot { return snap },
		preferences: func() (workspacePreferences, error) { return loadWorkspacePreferences(path) },
		savePreference: func(row workspaceSetting, value string) error {
			return saveWorkspacePreference(t.Context(), path, row, value)
		},
		guard: func() error { return errors.New("smoke fixture: no lab actions allowed") },
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := loadSettings(path)
	if err != nil || string(saved["parallel"]) != "2" || len(saved) != 1 {
		t.Fatalf("expected only parallel=2: %v, %v", saved, err)
	}
}

func TestSaveWorkspacePreference(t *testing.T) {
	for _, key := range []string{"parallel", "verbose", "keep-context", "update-check", "cache-retention-days", "cache-max-size-gb"} {
		t.Run(key, func(t *testing.T) {
			m := preferencesFixture(t, key)
			row := m.preferences.rows[m.preferences.selected]
			value := "2"
			if row.definition.Default == "true" || row.definition.Default == "false" {
				value = "false"
			}
			if err := saveWorkspacePreference(t.Context(), m.preferences.path, row, value); err != nil {
				t.Fatal(err)
			}
			saved, err := loadSettings(m.preferences.path)
			if err != nil || string(saved[key]) != value {
				t.Fatalf("save: %v %v", saved, err)
			}
			info, err := os.Stat(m.preferences.path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("settings permissions: %v", err)
			}
			if _, err := os.Stat(settingsReceiptPath(m.preferences.path)); !os.IsNotExist(err) {
				t.Fatal("settings edit created review consent")
			}
		})
	}
	m := preferencesFixture(t, "parallel")
	path := m.preferences.path
	row := m.preferences.rows[m.preferences.selected]
	other := "true"
	if err := changeSetting(path, "verbose", &other); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspacePreference(t.Context(), path, row, "3"); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSettings(path)
	if err != nil || string(saved["verbose"]) != "true" {
		t.Fatalf("lost unrelated edit: %v, %v", saved, err)
	}
	if err := saveWorkspacePreference(t.Context(), path, row, "2"); err == nil || !strings.Contains(err.Error(), "changed in another") {
		t.Fatalf("lost concurrent edit: %v", err)
	}
	if err := saveWorkspacePreference(t.Context(), path, row, "99"); err == nil {
		t.Fatal("accepted invalid value")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := saveWorkspacePreference(ctx, path, row, "2"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := withSettingsLock(path, func() error {
		if err := saveWorkspacePreference(t.Context(), path, row, "2"); err == nil {
			t.Fatal("bypassed settings lock")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspacePreferenceDriver(t *testing.T) {
	for _, failure := range []string{"", "load", "save", "reload"} {
		t.Run(failure, func(t *testing.T) {
			m := preferencesFixture(t, "parallel")
			prefs := m.preferences
			screens, loads, writes := 0, 0, 0
			d := workspaceDriver{
				screen: func(_ context.Context, m workspaceModel, _ func(context.Context) workspaceSnapshot) (workspaceModel, error) {
					screens++
					switch screens {
					case 1:
						m.action = workspaceLoadPreferences
					case 2:
						if !m.showPreferences {
							t.Fatal("preferences did not open")
						}
						if failure == "load" {
							if !strings.Contains(m.preferences.notice, "Could not load") {
								t.Fatal("missing load error")
							}
							m.action = workspaceLeave
						} else {
							m.preferences.draft, m.action = "2", workspaceSavePreference
						}
					default:
						if failure == "save" && (!m.preferences.editing || !strings.Contains(m.preferences.notice, "Not saved")) {
							t.Fatal("edit lost on failure")
						}
						if failure != "save" && !strings.Contains(m.preferences.notice, "Saved.") {
							t.Fatal("missing save confirmation")
						}
						if failure == "reload" && !strings.Contains(m.preferences.notice, "Could not load") {
							t.Fatal("missing reload error")
						}
						m.action = workspaceLeave
					}
					return m, nil
				},
				guard: func() error { t.Fatal("settings invoked a lab guard"); return nil },
				preferences: func() (workspacePreferences, error) {
					loads++
					if failure == "load" || (failure == "reload" && loads == 2) {
						return workspacePreferences{}, errors.New("unreadable")
					}
					return prefs, nil
				},
				savePreference: func(row workspaceSetting, value string) error {
					writes++
					if row.key != "parallel" || value != "2" {
						t.Fatal("wrong setting saved")
					}
					if failure == "save" {
						return errors.New("locked")
					}
					return nil
				},
			}
			if err := driveWorkspace(t.Context(), m, d); err != nil {
				t.Fatal(err)
			}
			if failure != "load" && writes != 1 {
				t.Fatal("wrong number of saves")
			}
		})
	}
}
