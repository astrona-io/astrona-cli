package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
)

func TestWorkspaceHomeKeys(t *testing.T) {
	for _, tc := range []struct {
		name     string
		keys     []string
		action   workspaceAction
		entering bool
	}{
		{"quit", []string{"q"}, workspaceLeave, false},
		{"interrupt", []string{"ctrl+c"}, workspaceLeave, false},
		{"preferences", []string{"p"}, workspaceLoadPreferences, false},
		{"refresh", []string{"r"}, workspaceReloadLabs, false},
		{"new", []string{"n"}, workspaceNone, true},
		{"empty enter", []string{"enter"}, workspaceNone, true},
		{"blank lab", []string{"n", "enter"}, workspaceNone, true},
		{"cancel", []string{"n", "./labs/demo", "esc"}, workspaceNone, false},
		{"local path", []string{"n", "./labs/demo", "enter"}, workspaceStart, false},
		{"catalog", []string{"n", "astrona.io/ATS014/section-010/module-01/lab-02", "enter"}, workspaceStart, false},
		{"lab actions ignored", []string{"s", "g", "d", "y"}, workspaceNone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := workspaceModel{home: true, width: 80, height: 24}
			if m.Init() != nil {
				t.Fatal("empty workspace started lab polling")
			}
			for _, key := range tc.keys {
				m = workspaceKey(m, key)
			}
			if m.action != tc.action || m.enteringLab != tc.entering || m.exitMenu {
				t.Fatalf("unexpected home state: %+v", m)
			}
		})
	}
	m := workspaceModel{home: true, width: 80, height: 24, labs: []string{"a", "b"}}
	m = workspaceKey(workspaceKey(m, "down"), "enter")
	if m.action != workspaceSelect || m.selected != 1 {
		t.Fatal("could not select refreshed lab")
	}
	m = workspaceModel{home: true, width: 80, height: 24, enteringLab: true, labDraft: "./ø"}
	m = preferenceKey(m, tea.KeyBackspace)
	if m.labDraft != "./" {
		t.Fatal("backspace corrupted the path")
	}
	m.width = 20
	m = workspaceKey(m, "enter")
	if m.action != workspaceNone {
		t.Fatal("started a lab from an unreadable screen")
	}
}

// Run explicitly from a terminal with an empty profile and no container engine.
func TestWorkspaceHomeTerminalSmoke(t *testing.T) {
	if os.Getenv("ASTRONA_WORKSPACE_HOME_SMOKE") != "1" || !isatty.IsTerminal(os.Stdin.Fd()) {
		t.Skip("manual terminal smoke test")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PATH", dir)
	cmd := newWorkspaceCmd(&rootFlags{settingsFile: filepath.Join(dir, "settings.json")})
	cmd.SetContext(t.Context())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceHomeView(t *testing.T) {
	for _, size := range [][2]int{{48, 14}, {80, 24}, {120, 40}} {
		for _, mode := range []string{"empty", "input", "labs"} {
			t.Run(mode, func(t *testing.T) {
				m := workspaceModel{home: true, width: size[0], height: size[1], enteringLab: mode == "input", notice: "notice\x1b[2J\x07", labDraft: strings.Repeat("long-path/", 100)}
				if mode == "labs" {
					m.labs = strings.Fields(strings.Repeat("lab ", 100))
					m.selected = 99
				}
				view := ansi.Strip(m.View())
				if strings.ContainsAny(view, "\x1b\x07") || strings.Contains(view, "Elapsed:") || strings.Contains(view, "SUBMISSION RESULTS") {
					t.Fatalf("unexpected home content: %s", view)
				}
				if len(strings.Split(view, "\n")) > m.height {
					t.Fatalf("view exceeds height: %s", view)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > m.width {
						t.Fatalf("view exceeds width: %q", line)
					}
				}
			})
		}
	}
	m := preferencesFixture(t, "update-check")
	m.home, m.labs = true, []string{"a"}
	if !strings.Contains(m.View(), "PREFERENCES") {
		t.Fatal("home hid preferences")
	}
	m = workspaceKey(m, "esc")
	if m.showPreferences || !m.home {
		t.Fatal("preferences did not return home")
	}
}
