package main

import (
	"context"
	"os"
	"path/filepath"
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

func TestDeletionReportsPlaygroundsWithoutSubmittingLabs(t *testing.T) {
	for _, mode := range []string{"cli", "ui"} {
		for _, kind := range []string{"lab", "playground", "local playground", "guest playground"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				site := newPlaygroundSite(t)
				site.signIn(t)
				started := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
				st := &labstate.State{StartedAt: started, Session: &labstate.Session{Site: site.srv.URL, ID: "session", Kind: "playground"}}
				if kind == "lab" {
					st.Session.Kind = "lab"
				}
				if kind == "local playground" {
					st.LocalOnly = true
				}
				if kind == "guest playground" {
					st.Session = nil
				}
				if err := labstate.Save("astro-play", st); err != nil {
					t.Fatal(err)
				}
				if _, err := labstate.RecordUsage("astro-play", started, 50, 1024, time.Now()); err != nil {
					t.Fatal(err)
				}
				// Fake the runtime so deletion exercises the real cleanup and API paths.
				bin := t.TempDir()
				for name, script := range map[string]string{
					"docker": "#!/bin/sh\nif [ \"$1\" = ps ]; then echo astro-play-control-plane; fi\n",
					"kind":   "#!/bin/sh\nexit 0\n",
				} {
					if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("PATH", bin)
				t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "docker")
				if mode == "ui" {
					if err := forceDeleteUIRun(context.Background(), uiHomeRun{name: "astro-play", runID: workspaceRunID(st)}, false); err != nil {
						t.Fatal(err)
					}
				} else {
					forgetLab("astro-play")
				}
				if saved, err := labstate.Load("astro-play"); err != nil || saved != nil {
					t.Fatalf("deleted state remains: %+v, %v", saved, err)
				}
				site.mu.Lock()
				defer site.mu.Unlock()
				wantStop := kind == "playground" || (kind == "lab" && mode == "cli")
				if !wantStop {
					if len(site.stopBodies) != 0 {
						t.Fatalf("discarded run sent data: %v", site.stopBodies)
					}
					return
				}
				if len(site.stopBodies) != 1 {
					t.Fatalf("stop requests = %v", site.stopBodies)
				}
				body := site.stopBodies[0]
				if body["reason"] != "destroyed" {
					t.Fatalf("stop reason: %v", body)
				}
				if kind == "lab" {
					if len(body) != 1 {
						t.Fatalf("unsubmitted lab sent data beyond session closure: %v", body)
					}
				} else {
					usage, ok := body["usage"].(map[string]any)
					if !ok || usage["samples"] != float64(1) {
						t.Fatalf("playground usage missing: %v", body)
					}
				}
			})
		}
	}
}

func TestUIDeleteExplainsTracking(t *testing.T) {
	for _, tc := range []struct {
		run  uiHomeRun
		want string
	}{
		{uiHomeRun{kind: "Lab"}, "No results will be submitted"},
		{uiHomeRun{kind: "Playground", reportPlayground: true}, "resource usage will be reported"},
		{uiHomeRun{kind: "Playground / QEMU"}, "No data will be sent"},
	} {
		if got := uiDeleteMessage(tc.run, false); !strings.Contains(got, tc.want) {
			t.Fatalf("warning = %s", got)
		}
	}
}
