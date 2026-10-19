package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

func TestGuestReminderChoicesAndPersistence(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		keys                   []tea.Msg
		login, accepted, never bool
	}{
		{"login", []tea.Msg{tea.KeyPressMsg{Code: tea.KeyEnter}}, true, true, false},
		{"guest once", []tea.Msg{tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyEnter}}, false, true, false},
		{"guest never", []tea.Msg{tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, tea.KeyPressMsg{Code: tea.KeyEnter}}, false, true, true},
		{"cancel checkbox", []tea.Msg{tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, tea.KeyPressMsg{Code: tea.KeyEscape}}, false, false, true},
		{"paste ignored", []tea.Msg{tea.PasteMsg{Content: "\r"}}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := &rootFlags{settingsFile: filepath.Join(t.TempDir(), "settings.json")}
			if enabled, err := guestReminderEnabled(flags); err != nil || !enabled {
				t.Fatal("reminder not enabled by default", err)
			}
			existing := "true"
			if err := changeSetting(flags.settingsFile, "verbose", &existing); err != nil {
				t.Fatal(err)
			}
			m := guestRunModel{width: 80, height: 24}
			for _, key := range tc.keys {
				model, _ := m.Update(key)
				m = model.(guestRunModel)
			}
			if m.choice != (guestRunChoice{tc.accepted, tc.login, tc.never}) {
				t.Fatalf("choice: %+v", m.choice)
			}
			if err := saveGuestChoice(flags, m.choice); err != nil {
				t.Fatal(err)
			}
			enabled, err := guestReminderEnabled(flags)
			if err != nil || enabled == (tc.accepted && tc.never) {
				t.Fatalf("saved reminder: %v %v", enabled, err)
			}
			saved, err := loadSettings(flags.settingsFile)
			if err != nil || string(saved["verbose"]) != "true" {
				t.Fatal("unrelated setting overwritten")
			}
			value := "yes"
			if err := changeSetting(flags.settingsFile, "login-reminder", &value); err != nil {
				t.Fatal(err)
			}
			if enabled, err := guestReminderEnabled(flags); err != nil || !enabled {
				t.Fatal("cannot restore reminder", err)
			}
		})
	}
}

func TestGuestReminderLayoutAndCancel(t *testing.T) {
	for _, size := range [][2]int{{48, 14}, {80, 24}, {120, 35}} {
		m := guestRunModel{width: size[0], height: size[1]}
		view := ansi.Strip(m.render())
		for _, want := range []string{"Log in", "Continue as guest", "Never ask again"} {
			if !strings.Contains(view, want) {
				t.Fatalf("missing %q at %v: %s", want, size, view)
			}
		}
	}
	m := guestRunModel{width: 20, height: 8}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.(guestRunModel).choice.accepted || cmd != nil {
		t.Fatal("accepted hidden prompt")
	}
}

func TestGuestReminderLocalOnlySkipsAccountAndSettings(t *testing.T) {
	flags := &rootFlags{localOnly: true, settingsFile: "/nonexistent/settings.json"}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	acct, approved, err := confirmGuestLabRun(cmd, flags, nil, false)
	if err != nil || !approved || acct != nil {
		t.Fatalf("local-only: %v %v %v", acct, approved, err)
	}
}

func TestLoginReminderDefinitionIsBoolean(t *testing.T) {
	raw, err := parseSetting("login-reminder", "no")
	if err != nil || string(raw) != "false" {
		t.Fatal("invalid boolean setting", err)
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil || enabled {
		t.Fatal("not stored as boolean")
	}
}
