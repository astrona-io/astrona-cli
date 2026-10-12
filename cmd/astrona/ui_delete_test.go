package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"astrona/internal/labstate"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestUIHomeDeleteRequiresExplicitYes(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want bool
	}{
		{"default cancels", []string{"ctrl+d", "enter"}, false},
		{"escape cancels", []string{"ctrl+d", "y", "esc"}, false},
		{"interrupt cancels", []string{"ctrl+d", "y", "ctrl+c"}, false},
		{"yes only selects", []string{"ctrl+d", "y"}, false},
		{"yes enter accepts", []string{"ctrl+d", "y", "enter"}, true},
		{"tab enter accepts", []string{"ctrl+d", "tab", "enter"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := uiHomeModel{width: 100, height: 30, data: uiHomeSnapshot{runs: []uiHomeRun{{name: "astro-lab", runID: "original", openable: true}}}}
			for _, key := range tc.keys {
				m = homeKey(m, key)
			}
			if m.destroy != tc.want || m.quit {
				t.Fatalf("destroy=%v quit=%v", m.destroy, m.quit)
			}
		})
	}
}

func TestUIDeleteKeepsApprovedRunAcrossRefresh(t *testing.T) {
	run := uiHomeRun{name: "astro-lab", runID: "original", openable: true}
	m := uiHomeModel{width: 100, height: 30, data: uiHomeSnapshot{runs: []uiHomeRun{run}}}
	m = homeKey(m, "ctrl+d")
	view := ansi.Strip(m.View())
	for _, want := range []string{"FORCE DELETE", "astro-lab", "will be lost", "No, keep running"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing warning %q", want)
		}
	}
	next, _ := m.Update(uiHomeSnapshot{runs: []uiHomeRun{{name: "astro-other", runID: "new", openable: true}}})
	m = homeKey(homeKey(next.(uiHomeModel), "y"), "enter")
	if !m.destroy || *m.deleting != run {
		t.Fatal("refresh changed the deletion target")
	}
}

func TestUIDeleteCannotConfirmHiddenDialogOrPastedInput(t *testing.T) {
	m := uiHomeModel{width: 100, height: 30, data: uiHomeSnapshot{runs: []uiHomeRun{{name: "astro-lab"}}}}
	m = homeKey(m, "ctrl+d")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y"), Paste: true})
	if next.(uiHomeModel).deleteYes {
		t.Fatal("paste selected destruction")
	}
	m = homeKey(m, "y")
	next, _ = m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	m = homeKey(next.(uiHomeModel), "enter")
	if m.destroy {
		t.Fatal("confirmed hidden warning after resize")
	}
}

func TestUIForceDeleteRejectsReplacedRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st := &labstate.State{StartedAt: time.Now().UTC().Format(time.RFC3339), Source: &labstate.Source{Config: "/tmp/lab"}}
	if err := labstate.Save("astro-lab", st); err != nil {
		t.Fatal(err)
	}
	err := forceDeleteUIRun(context.Background(), uiHomeRun{name: "astro-lab", runID: "old"}, false)
	if err == nil || !strings.Contains(err.Error(), "nothing was deleted") {
		t.Fatal(err)
	}
	after, err := labstate.Load("astro-lab")
	if err != nil || workspaceRunID(after) != workspaceRunID(st) || after.LocalOnly {
		t.Fatal("stale confirmation modified replacement")
	}
}
