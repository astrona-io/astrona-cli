package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/labstate"
	"astrona/internal/proctor"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
)

func homeKey(m uiHomeModel, key string) uiHomeModel {
	types := map[string]tea.KeyType{"tab": tea.KeyTab, "enter": tea.KeyEnter, "esc": tea.KeyEsc, "down": tea.KeyDown, "up": tea.KeyUp}
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	if kind, ok := types[key]; ok {
		msg.Type = kind
	}
	next, _ := m.Update(msg)
	return next.(uiHomeModel)
}

func TestHomeEmptyHistoryAndNavigation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	data := readUIHome(context.Background())
	if len(data.runs) != 0 || data.warning != "" {
		t.Fatalf("empty Home needs an engine: %+v", data)
	}
	m := uiHomeModel{data: data, width: 100, height: 24}
	if !strings.Contains(m.View(), "No active runs") {
		t.Fatal(m.View())
	}
	m = homeKey(m, "tab")
	if !strings.Contains(m.View(), "No history yet") {
		t.Fatal(m.View())
	}
	if got := homeKey(m, "enter"); got.open || got.detail {
		t.Fatal("empty History opened a run or detail")
	}
	if got := homeKey(m, "q"); !got.quit || got.open {
		t.Fatal("Home quit performed an action")
	}
	if err := proctor.RecordAttempt("astro-lab", proctor.Attempt{Time: time.Now(), Pass: true, Earned: 2, Max: 2, Checks: []proctor.AttemptCheck{{Name: "safe\x1b[2J", Pass: true}}}); err != nil {
		t.Fatal(err)
	}
	st := &labstate.State{Source: &labstate.Source{Catalog: "ATS/x/playground"}, StartedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}
	if err := labstate.RecordFinishedRun("astro-play", st, time.Now()); err != nil {
		t.Fatal(err)
	}
	m.data = readUIHome(context.Background())
	if len(m.data.history) != 2 || m.data.warning != "" {
		t.Fatalf("offline history missing: %+v", m.data)
	}
	m = homeKey(m, "enter")
	if !m.detail || m.open || strings.Contains(m.View(), "\x1b[2J") {
		t.Fatal("unsafe history detail or lifecycle action")
	}
}

func TestHomeSelectionRefreshAndLayout(t *testing.T) {
	m := uiHomeModel{data: uiHomeSnapshot{runs: []uiHomeRun{{name: "b", openable: true}}}, width: 100, height: 24}
	next, _ := m.Update(uiHomeSnapshot{runs: []uiHomeRun{{name: "a"}, {name: "b", openable: true}}})
	m = next.(uiHomeModel)
	if m.selected != 1 || !homeKey(m, "enter").open {
		t.Fatal("refresh changed selected run")
	}
	m.data.runs[1].openable = false
	if got := homeKey(m, "enter"); got.open || got.notice == "" {
		t.Fatal("unavailable run was opened")
	}
	for _, size := range []struct{ w, h int }{{100, 30}, {48, 14}, {20, 8}} {
		for tab := range 3 {
			m.tab, m.width, m.height = tab, size.w, size.h
			view := ansi.Strip(m.View())
			if len(strings.Split(view, "\n")) > size.h {
				t.Fatal("Home overflows height")
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size.w {
					t.Fatalf("Home overflows width: %q", line)
				}
			}
		}
	}
}

func TestHomeReturnsAfterWorkspaceAndHandlesErrors(t *testing.T) {
	for _, failure := range []error{nil, errors.New("run changed elsewhere")} {
		screens, opens := 0, 0
		d := uiHomeDriver{
			screen: func(_ context.Context, m uiHomeModel) (uiHomeModel, error) {
				screens++
				if screens == 1 {
					m.data.runs = []uiHomeRun{{name: "astro-lab", openable: true}}
					m.open = true
				} else {
					if m.notice == "" {
						t.Fatal("workspace outcome missing on Home")
					}
					m.quit = true
				}
				return m, nil
			},
			open: func(uiHomeRun) (string, error) { opens++; return "Submitted", failure },
		}
		if err := driveUIHome(context.Background(), uiHomeModel{}, d); err != nil || screens != 2 || opens != 1 {
			t.Fatalf("did not return Home: %d %d %v", screens, opens, err)
		}
	}
}

func TestHomePreferencesAndStartup(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			prefs := preferencesFixture(t, "update-check").preferences
			screens, saves, starts := 0, 0, 0
			d := uiHomeDriver{
				screen: func(_ context.Context, m uiHomeModel) (uiHomeModel, error) {
					screens++
					m.width, m.height = 80, 24
					switch screens {
					case 1:
						m = homeKey(m, "p")
					case 2:
						if !m.editor.showPreferences {
							t.Fatal("preferences did not open")
						}
						for range prefs.selected {
							m = homeKey(m, "down")
						}
						m = homeKey(homeKey(homeKey(homeKey(m, "enter"), "no"), "enter"), "y")
					case 3:
						if !strings.Contains(m.editor.preferences.notice, "Saved.") {
							t.Fatal("save result missing")
						}
						m = homeKey(homeKey(homeKey(homeKey(m, "esc"), "n"), "./labs/demo"), "enter")
					case 4:
						if m.editor.enteringLab || m.editor.showPreferences || m.notice == "" || m.noticeError != failure {
							t.Fatal("startup did not return Home")
						}
						m = homeKey(m, "q")
					default:
						t.Fatal("unexpected extra screen")
					}
					return m, nil
				},
				preferences: func() (workspacePreferences, error) { return prefs, nil },
				savePreference: func(row workspaceSetting, value string) error {
					saves++
					if row.key != "update-check" || value != "false" {
						t.Fatal("wrong preference saved")
					}
					return nil
				},
				start: func(lab string) error {
					starts++
					if lab != "./labs/demo" {
						t.Fatal(lab)
					}
					if failure {
						return errors.New("startup failed")
					}
					return nil
				},
			}
			if err := driveUIHome(t.Context(), uiHomeModel{}, d); err != nil || saves != 1 || starts != 1 {
				t.Fatalf("save=%d start=%d err=%v", saves, starts, err)
			}
		})
	}
}

func TestWorkspaceCompletionReturnsHomeWithoutExtraEffects(t *testing.T) {
	for _, action := range []workspaceAction{workspaceSubmit, workspaceDestroy, workspaceLeave} {
		for _, gradeErr := range []error{nil, notPassed("try again")} {
			screens, submitted, destroyed, returned := 0, 0, 0, ""
			d := workspaceDriver{
				screen: func(_ context.Context, m workspaceModel, _ func(context.Context) workspaceSnapshot) (workspaceModel, error) {
					screens++
					if screens > 1 {
						t.Fatal("completion reopened workspace instead of Home")
					}
					m.action = action
					return m, nil
				},
				guard:      func() error { return nil },
				returnHome: func(message string) { returned = message },
				submit:     func() (string, error) { submitted++; return "Attempt #1", gradeErr },
				destroy:    func() (bool, error) { destroyed++; return true, nil },
			}
			if err := driveWorkspace(context.Background(), workspaceModel{name: "astro-lab", canSubmit: true}, d); err != nil || returned == "" {
				t.Fatalf("completion not returned: %q %v", returned, err)
			}
			if (action == workspaceSubmit && (submitted != 1 || destroyed != 0)) || (action == workspaceLeave && (submitted != 0 || destroyed != 0)) || (action == workspaceDestroy && (submitted != 0 || destroyed != 1)) {
				t.Fatal("unexpected lifecycle effect")
			}
		}
	}
}

func TestHomeLiveDiscoveryExcludesLinkedAndTestRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	engine := "#!/bin/sh\n[ \"$1\" = ps ] || exit 1\nprintf '%s\\n' 'astro-main-control-plane|running|astro-main|control-plane' 'astro-main-idp-control-plane|running|astro-main-idp|control-plane' 'astro-test-author-control-plane|running|astro-test-author|control-plane'\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(engine), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "docker")
	for _, name := range []string{"astro-main", "astro-main-idp", "astro-test-author"} {
		if err := labstate.Save(name, &labstate.State{Source: &labstate.Source{Config: "lab"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cluster.WriteLinks("astro-main", []cluster.LinkState{{Name: "idp", Cluster: "astro-main-idp"}}); err != nil {
		t.Fatal(err)
	}
	data := readUIHome(context.Background())
	if data.warning != "" || len(data.runs) != 1 || data.runs[0].name != "astro-main" || !data.runs[0].openable {
		t.Fatalf("incorrect active runs: %+v", data)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readHomeContainers(ctx); err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
}

func TestForgetLabPreservesPlaygroundHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st := &labstate.State{Source: &labstate.Source{Config: "playground"}, StartedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}
	if err := labstate.Save("astro-play", st); err != nil {
		t.Fatal(err)
	}
	forgetLab("astro-play")
	if active, err := labstate.Load("astro-play"); err != nil || active != nil {
		t.Fatalf("active state retained: %+v, %v", active, err)
	}
	runs, err := labstate.FinishedRuns()
	if err != nil || len(runs) != 1 || runs[0].Kind != "Playground" {
		t.Fatalf("teardown lost history: %+v, %v", runs, err)
	}
}

// Opt-in real terminal check; fixtures never touch real containers or accounts.
func TestUIHomeTerminalSmoke(t *testing.T) {
	if os.Getenv("ASTRONA_UI_HOME_SMOKE") != "1" || !isatty.IsTerminal(os.Stdin.Fd()) {
		t.Skip("manual terminal smoke test")
	}
	t.Setenv("HOME", t.TempDir())
	if err := proctor.RecordAttempt("astro-demo", proctor.Attempt{Time: time.Now(), Pass: true, Earned: 2, Max: 2, Checks: []proctor.AttemptCheck{{Name: "example check", Pass: true}}}); err != nil {
		t.Fatal(err)
	}
	final, err := runUIHomeScreen(context.Background(), uiHomeModel{})
	if err != nil || !final.quit || final.open {
		t.Fatalf("Home terminal navigation: %+v, %v", final, err)
	}
}
