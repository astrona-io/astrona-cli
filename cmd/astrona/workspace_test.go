package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labstate"
	"astrona/internal/proctor"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
)

func workspaceKey(m workspaceModel, key string) workspaceModel {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		msg.Type = tea.KeyEnter
	case "esc":
		msg.Type = tea.KeyEsc
	case "tab":
		msg.Type = tea.KeyTab
	case "ctrl+b":
		msg.Type = tea.KeyCtrlB
	case "ctrl+c":
		msg.Type = tea.KeyCtrlC
	case "ctrl+d":
		msg.Type = tea.KeyCtrlD
	case "down":
		msg.Type = tea.KeyDown
	case "up":
		msg.Type = tea.KeyUp
	case "pgdown":
		msg.Type = tea.KeyPgDown
	case "pgup":
		msg.Type = tea.KeyPgUp
	case "home":
		msg.Type = tea.KeyHome
	}
	next, _ := m.Update(msg)
	return next.(workspaceModel)
}

func TestWorkspaceActions(t *testing.T) {
	for _, tt := range []struct {
		name            string
		keys            []string
		graded, blocked bool
		want            workspaceAction
	}{
		{"shell", []string{"s"}, true, false, workspaceShell},
		{"preferences", []string{"p"}, true, false, workspaceLoadPreferences},
		{"stale can edit preferences", []string{"p"}, true, true, workspaceLoadPreferences},
		{"enter keeps workspace", []string{"enter"}, true, false, workspaceNone},
		{"submit", []string{"g"}, true, false, workspaceSubmit},
		{"playground ignores grade", []string{"g"}, false, false, workspaceNone},
		{"q only opens menu", []string{"q"}, true, false, workspaceNone},
		{"back returns home", []string{"ctrl+b"}, true, false, workspaceLeave},
		{"interrupt returns home", []string{"ctrl+c"}, true, false, workspaceLeave},
		{"control d asks before destruction", []string{"ctrl+d"}, true, false, workspaceNone},
		{"enter returns", []string{"q", "enter"}, true, false, workspaceNone},
		{"leave", []string{"q", "k"}, true, false, workspaceLeave},
		{"menu submit", []string{"q", "s"}, true, false, workspaceSubmit},
		{"playground menu", []string{"q", "s"}, false, false, workspaceNone},
		{"destroy needs confirmation", []string{"q", "d"}, true, false, workspaceNone},
		{"destroy enter cancels", []string{"q", "d", "enter"}, true, false, workspaceNone},
		{"destroy n cancels", []string{"q", "d", "n"}, true, false, workspaceNone},
		{"destroy confirmed", []string{"q", "d", "y"}, true, false, workspaceDestroy},
		{"no direct destruction", []string{"d", "y"}, true, false, workspaceNone},
		{"stale shell", []string{"s"}, true, true, workspaceNone},
		{"stale submit", []string{"g"}, true, true, workspaceNone},
		{"stale destroy", []string{"q", "d", "y"}, true, true, workspaceNone},
		{"stale can leave", []string{"q", "k"}, true, true, workspaceLeave},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := workspaceModel{canSubmit: tt.graded, snapshot: workspaceSnapshot{clusters: []workspaceCluster{{name: "astro-lab"}}}}
			if tt.blocked {
				m.snapshot.blocked = "Run changed"
			}
			for _, key := range tt.keys {
				m = workspaceKey(m, key)
			}
			if m.action != tt.want {
				t.Fatalf("action %v, want %v", m.action, tt.want)
			}
		})
	}
}

func TestWorkspaceSelectionAndScrolling(t *testing.T) {
	m := workspaceModel{height: 24, snapshot: workspaceSnapshot{clusters: []workspaceCluster{{name: "astro-main"}, {name: "astro-linked", alias: "idp"}}}}
	for range 5 {
		m = workspaceKey(m, "down")
	}
	if m.selected != 1 {
		t.Fatal(m.selected)
	}
	for range 5 {
		m = workspaceKey(m, "up")
	}
	if m.selected != 0 {
		t.Fatal(m.selected)
	}
	m = workspaceKey(m, "pgdown")
	if m.scroll != 12 {
		t.Fatal(m.scroll)
	}
	m = workspaceKey(m, "tab")
	if !m.results || m.scroll != 0 {
		t.Fatal("tab must reset scroll")
	}
	m = workspaceKey(workspaceKey(m, "pgdown"), "pgup")
	if m.scroll != 0 {
		t.Fatal(m.scroll)
	}
	m = workspaceKey(workspaceKey(m, "pgdown"), "home")
	if m.scroll != 0 {
		t.Fatal(m.scroll)
	}
	m = workspaceModel{labs: []string{"a", "b"}}
	if m.Init() != nil {
		t.Fatal("picker must not poll")
	}
	m = workspaceKey(workspaceKey(m, "down"), "enter")
	if m.action != workspaceSelect || m.selected != 1 {
		t.Fatal("wrong selection")
	}
	if got := workspaceKey(workspaceModel{labs: []string{"a"}}, "q"); got.action != workspaceLeave {
		t.Fatal(got.action)
	}
}

func TestWorkspacePollingNeverActs(t *testing.T) {
	m := workspaceModel{refresh: func() tea.Msg { return workspaceSnapshot{} }, now: time.Now()}
	updated, cmd := m.Update(workspaceTick(time.Now()))
	m = updated.(workspaceModel)
	if !m.polling || cmd == nil || m.action != workspaceNone {
		t.Fatal("expected read-only refresh")
	}
	updated, _ = m.Update(workspaceSnapshot{clusters: []workspaceCluster{{name: "astro-lab"}}})
	m = updated.(workspaceModel)
	if m.polling || !m.nextRefresh.After(time.Now()) {
		t.Fatal("refresh must be throttled")
	}
	updated, _ = m.Update(workspaceTick(time.Now()))
	if updated.(workspaceModel).polling {
		t.Fatal("polled too soon")
	}
}

func TestWorkspaceTerminalValidation(t *testing.T) {
	for _, tt := range []struct {
		name              string
		input, output, ci bool
		term, nested      string
		ok                bool
	}{
		{"terminal", true, true, false, "xterm-256color", "", true},
		{"input pipe", false, true, false, "xterm", "", false},
		{"output pipe", true, false, false, "xterm", "", false},
		{"ci tty", true, true, true, "xterm", "", false},
		{"dumb", true, true, false, "dumb", "", false},
		{"lab shell can open UI", true, true, false, "xterm", "astro-lab", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(labShellEnvVar, tt.nested)
			if err := checkWorkspaceTerminal(tt.input, tt.output, tt.ci, tt.term); (err == nil) != tt.ok {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkspaceRuntimeAndGrading(t *testing.T) {
	cfg := &config.LabConfig{}
	if err := workspaceRuntime(cfg); err != nil {
		t.Fatal(err)
	}
	if workspaceCanSubmit(cfg, false) {
		t.Fatal("empty playground cannot submit")
	}
	cfg.Validation.Checks = []config.ValidationCheck{{Name: "check"}}
	if !workspaceCanSubmit(cfg, false) || workspaceCanSubmit(cfg, true) {
		t.Fatal("grading availability")
	}
	cfg.Runtime.Type = "qemu"
	if err := workspaceRuntime(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Runtime.Type = "unknown"
	if err := workspaceRuntime(cfg); err == nil {
		t.Fatal("unsupported runtime accepted")
	}
}

func TestWorkspaceRunFlagDefaultsOffAndRejectsCI(t *testing.T) {
	t.Setenv("CI", "true")
	flags := &rootFlags{}
	run := newRunCmd(flags)
	if run.Flags().Lookup("ui").DefValue != "false" {
		t.Fatal("UI must be opt-in")
	}
	if err := run.Flags().Set("ui", "true"); err != nil {
		t.Fatal(err)
	}
	// No config, no account or runtime should be touched before this error.
	if err := run.RunE(run, nil); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatal(err)
	}
	cmd := newWorkspaceCmd(flags)
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatal(err)
	}
}

func TestWorkspaceStateAndTimers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC().Truncate(time.Second)
	st := &labstate.State{StartedAt: now.Add(-time.Minute).Format(time.RFC3339), Source: &labstate.Source{Config: "/tmp/lab"}, Session: &labstate.Session{ID: "s1", ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)}}
	if err := labstate.Save("astro-lab", st); err != nil {
		t.Fatal(err)
	}
	runID := workspaceRunID(st)
	if err := workspaceGuard("astro-lab", runID); err != nil {
		t.Fatal("token expiry is not a lab deadline:", err)
	}
	snap := readWorkspaceSnapshot(context.Background(), "astro-lab", runID, nil, false, false)
	if !snap.started.Equal(now.Add(-time.Minute)) || !snap.deadline.IsZero() || snap.blocked != "" {
		t.Fatalf("bad clock: %+v", snap)
	}
	if err := exam.Start("astro-lab", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	snap = readWorkspaceSnapshot(context.Background(), "astro-lab", runID, nil, false, true)
	if !snap.deadline.Equal(now.Add(time.Hour)) {
		t.Fatal("exam deadline changed")
	}
	if err := proctor.RecordAttempt("astro-lab", proctor.Attempt{Time: now, Pass: false, Earned: 1, Max: 2, Checks: []proctor.AttemptCheck{{Name: "failed check"}}}); err != nil {
		t.Fatal(err)
	}
	snap = readWorkspaceSnapshot(context.Background(), "astro-lab", runID, nil, false, false)
	if !strings.Contains(snap.result, "FAIL  1/2") || !strings.Contains(snap.result, "failed check") {
		t.Fatal(snap.result)
	}
	if err := proctor.RecordAttempt("astro-lab", proctor.Attempt{Time: now, Pass: true, Earned: 2, Max: 2}); err != nil {
		t.Fatal(err)
	}
	snap = readWorkspaceSnapshot(context.Background(), "astro-lab", runID, nil, false, false)
	if !strings.Contains(snap.result, "Attempt #2  PASS") {
		t.Fatal(snap.result)
	}
	st.Session.Kind = "playground"
	st.Session.DeadlineAt = now.Add(-time.Second).Format(time.RFC3339)
	if err := labstate.Save("astro-lab", st); err != nil {
		t.Fatal(err)
	}
	if err := workspaceGuard("astro-lab", runID); err == nil {
		t.Fatal("expired playground allowed")
	}
	snap = readWorkspaceSnapshot(context.Background(), "astro-lab", runID, nil, true, false)
	if snap.blocked == "" || snap.kind != "Playground" || snap.result != "Playgrounds are not graded." {
		t.Fatalf("%+v", snap)
	}
	st.Session.ID = "s2"
	if err := labstate.Save("astro-lab", st); err != nil {
		t.Fatal(err)
	}
	if err := workspaceGuard("astro-lab", runID); err == nil {
		t.Fatal("replacement run allowed")
	}
	if err := labstate.Remove("astro-lab"); err != nil {
		t.Fatal(err)
	}
	if err := workspaceGuard("astro-lab", runID); err == nil {
		t.Fatal("destroyed run allowed")
	}
}

func TestWorkspaceViewSanitizesAndKeepsActionsVisible(t *testing.T) {
	for _, size := range []struct{ w, h int }{{120, 35}, {80, 24}, {48, 14}, {20, 8}} {
		m := workspaceModel{width: size.w, height: size.h, name: "astro-lab\x1b[2J", task: strings.Repeat("Task line\n", 100), now: time.Now(), canSubmit: true, snapshot: workspaceSnapshot{kind: "Lab", clusters: []workspaceCluster{{name: "astro-main", status: "Ready (1/1)"}}}}
		view := ansi.Strip(m.View())
		if size.w >= 48 && size.h >= 14 {
			lines := strings.Split(view, "\n")
			if len(lines) > size.h {
				t.Fatalf("%dx%d: %d lines overflow", size.w, size.h, len(lines))
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size.w {
					t.Fatalf("%dx%d: line overflows: %q", size.w, size.h, line)
				}
			}
		}
		if strings.Contains(m.View(), "\x1b[2J") || !strings.Contains(view, "q exit menu") {
			t.Fatal("unsafe or missing navigation")
		}
		m.exitMenu = true
		if !strings.Contains(ansi.Strip(m.View()), "leave running") {
			t.Fatal("no safe exit")
		}
		m.confirmDestroy = true
		if !strings.Contains(ansi.Strip(m.View()), "enter cancel") {
			t.Fatal("destructive default")
		}
	}
}

func TestWorkspaceAlertNavigation(t *testing.T) {
	m := workspaceModel{width: 80, height: 24, home: true, notice: strings.Repeat("A long error message.\n", 50) + "Final detail\x1b[2J"}
	m = workspaceKey(m, "!")
	if !m.showAlert {
		t.Fatal("alert did not open")
	}
	for _, key := range []string{"n", "p", "y", "d"} {
		m = workspaceKey(m, key)
		if m.action != workspaceNone || m.showPreferences {
			t.Fatal("alert key triggered a background action")
		}
	}
	for range 30 {
		m = workspaceKey(m, "pgdown")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Final detail") || !strings.Contains(view, "Esc close") || strings.Contains(m.View(), "\x1b[2J") {
		t.Fatalf("alert lost details or navigation: %s", view)
	}
	if len(strings.Split(view, "\n")) != m.height {
		t.Fatal("alert exceeded the terminal")
	}
	m = workspaceKey(m, "esc")
	if m.showAlert || !m.home || m.notice == "" {
		t.Fatal("closing the alert lost workspace state")
	}
	m = preferencesFixture(t, "parallel")
	m.preferences.notice = "Saved. Applies to future commands."
	if m.alertMessage() != "" {
		t.Fatal("save confirmation shown as an alert")
	}
	m.preferences.notice, m.preferences.noticeError, m.preferences.editing = "Invalid value", true, true
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m = next.(workspaceModel)
	if m.showAlert || !strings.Contains(m.preferences.draft, "!") {
		t.Fatal("alert shortcut consumed setting input")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if !next.(workspaceModel).showAlert {
		t.Fatal("could not inspect error while editing")
	}
}

func TestWorkspaceTaskAndOutputBound(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.LabConfig{}
	flags := &rootFlags{fileName: "config.yaml"}
	if got := workspaceTask(cfg, dir, flags); !strings.Contains(got, "No task") {
		t.Fatal(got)
	}
	cfg.Metadata.Docs.Question = "question.md"
	if err := os.WriteFile(filepath.Join(dir, "question.md"), []byte("---\ntitle: Test\n---\nTask\x1b[2J"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := workspaceTask(cfg, dir, flags); !strings.Contains(got, "Test") || strings.Contains(got, "\x1b") {
		t.Fatal(got)
	}
	cfg.Metadata.Docs.Question = "../outside.md"
	if got := workspaceTask(cfg, dir, flags); !strings.Contains(got, "Could not read") {
		t.Fatal(got)
	}
	var b workspaceOutput
	if _, err := b.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(make([]byte, 1024*1024)); err == nil {
		t.Fatal("unbounded output")
	}
	if b.Len() != 2 {
		t.Fatal("overflow retained")
	}
	if _, err := io.Copy(&b, strings.NewReader(strings.Repeat("x", 1024*1024))); err == nil {
		t.Fatal("io.Copy bypassed output limit")
	}
	t.Setenv("HOME", t.TempDir())
	if status := workspaceClusterHealth(context.Background(), "astro-missing"); status != "No isolated kubeconfig" {
		t.Fatal(status)
	}
}

func workspaceKubeconfigFixture(t *testing.T, name string) string {
	t.Helper()
	path, err := cluster.KubeconfigPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("apiVersion: v1\nkind: Config\ncurrent-context: kind-"+name+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkspaceHealthUsesIsolatedContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := workspaceKubeconfigFixture(t, "astro-lab")
	// The fake accepts only the complete, isolated read-only invocation.
	script := `#!/bin/sh
[ "$1" = --kubeconfig ] || exit 1
[ "$2" = "$EXPECTED_KUBECONFIG" ] || exit 1
[ "$3" = --context ] || exit 1
[ "$4" = kind-astro-lab ] || exit 1
[ "$5" = --request-timeout=3s ] || exit 1
[ "$6 $7 $8 $9" = 'get nodes -o json' ] || exit 1
echo '{"items":[{"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}'
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("EXPECTED_KUBECONFIG", path)
	t.Setenv("KUBECONFIG", "/must/not/use/global/kubeconfig")
	if got := workspaceClusterHealth(context.Background(), "astro-lab"); got != "Ready (1/1)" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := workspaceClusterHealth(ctx, "astro-lab"); got != "Unavailable (API/engine)" {
		t.Fatal(got)
	}
}

// Run explicitly from a real terminal; no clusters or user state are touched.
func TestWorkspaceTerminalSmoke(t *testing.T) {
	if os.Getenv("ASTRONA_WORKSPACE_SMOKE") != "1" || !isatty.IsTerminal(os.Stdin.Fd()) {
		t.Skip("manual terminal smoke test")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	stubTerminalContainer(t)
	workspaceKubeconfigFixture(t, "astro-demo")
	workspaceKubeconfigFixture(t, "astro-demo-idp")
	if err := cluster.WriteLinks("astro-demo", []cluster.LinkState{{Name: "idp", Cluster: "astro-demo-idp"}}); err != nil {
		t.Fatal(err)
	}
	snap := workspaceSnapshot{kind: "Lab", tracking: "Local-only smoke fixture", started: time.Now().Add(-5 * time.Minute), result: "No submissions recorded.", clusters: []workspaceCluster{{name: "astro-demo", status: "Ready (1/1)"}, {name: "astro-demo-idp", alias: "idp", status: "Ready (1/1)"}}}
	m := workspaceModel{name: "astro-demo", task: "# Configure the cluster\n\nSelect idp and press s. Check $KUBECONFIG in the shell, then exit.\n\nPress q, d, Enter to cancel destruction; q, k to leave.", snapshot: snap, canSubmit: true}
	for {
		var err error
		m, err = runWorkspaceScreen(context.Background(), m, func(context.Context) workspaceSnapshot { return snap })
		if err != nil {
			t.Fatal(err)
		}
		if m.action == workspaceLeave {
			return
		}
		if m.action == workspaceShell {
			if err := runLabShell(m.name, snap.clusters[m.selected].alias, nil); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Fatalf("unexpected lifecycle action: %v", m.action)
		}
	}
}

func TestWorkspaceRejectsMissingState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := workspaceGuard("../escape", "anything"); err == nil {
		t.Fatal("path escape")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runWorkspaceScreen(ctx, workspaceModel{}, nil)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled UI: %v", err)
	}
}

func TestWorkspaceDriverSafety(t *testing.T) {
	for _, tt := range []struct {
		name                               string
		action                             workspaceAction
		graded, blocked, destroyed         bool
		actionErr                          error
		wantSubmit, wantDestroy, wantShell int
	}{
		{name: "leave", action: workspaceLeave},
		{name: "signal exit", action: workspaceNone},
		{name: "linked shell", action: workspaceShell, wantShell: 1},
		{name: "failed shell", action: workspaceShell, actionErr: errors.New("shell failed"), wantShell: 1},
		{name: "failed grade stays", action: workspaceSubmit, graded: true, actionErr: notPassed("failed"), wantSubmit: 1},
		{name: "passed grade stays", action: workspaceSubmit, graded: true, wantSubmit: 1},
		{name: "grading error stays", action: workspaceSubmit, graded: true, actionErr: errors.New("grading unavailable"), wantSubmit: 1},
		{name: "playground refuses submit", action: workspaceSubmit},
		{name: "destroy confirmed", action: workspaceDestroy, destroyed: true, wantDestroy: 1},
		{name: "keepCluster", action: workspaceDestroy, wantDestroy: 1},
		{name: "destroy failure", action: workspaceDestroy, actionErr: errors.New("engine unavailable"), wantDestroy: 1},
		{name: "restart blocks submit", action: workspaceSubmit, graded: true, blocked: true},
		{name: "restart blocks destroy", action: workspaceDestroy, blocked: true},
		{name: "restart blocks shell", action: workspaceShell, blocked: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			submit, destroy, shell, screens := 0, 0, 0, 0
			m := workspaceModel{name: "astro-main", canSubmit: tt.graded, selected: 1, snapshot: workspaceSnapshot{clusters: []workspaceCluster{{name: "astro-main"}, {name: "astro-linked", alias: "idp"}}}}
			d := workspaceDriver{
				screen: func(_ context.Context, m workspaceModel, _ func(context.Context) workspaceSnapshot) (workspaceModel, error) {
					screens++
					if screens == 1 {
						m.action = tt.action
					} else {
						if tt.wantSubmit == 1 && (!m.results || m.submission != "graded result") {
							t.Fatal("submission not shown")
						}
						if tt.blocked && m.snapshot.blocked == "" {
							t.Fatal("missing stale run warning")
						}
						if tt.actionErr != nil && exitCodeFor(tt.actionErr) != 2 && m.notice == "" {
							t.Fatal("missing error")
						}
						m.action = workspaceLeave
					}
					return m, nil
				},
				guard: func() error {
					if tt.blocked {
						return errors.New("replaced run")
					}
					return nil
				},
				submit: func() (string, error) { submit++; return "graded result", tt.actionErr },
				shell: func(alias string) error {
					shell++
					if alias != "idp" {
						t.Fatal("wrong cluster", alias)
					}
					return tt.actionErr
				},
				destroy: func() (bool, error) { destroy++; return tt.destroyed, tt.actionErr },
			}
			if err := driveWorkspace(context.Background(), m, d); err != nil {
				t.Fatal(err)
			}
			if submit != tt.wantSubmit || destroy != tt.wantDestroy || shell != tt.wantShell {
				t.Fatalf("submit=%d destroy=%d shell=%d", submit, destroy, shell)
			}
			if (tt.action == workspaceSubmit || tt.action == workspaceShell) && screens != 2 {
				t.Fatal("action did not return to workspace")
			}
		})
	}
	d := workspaceDriver{screen: func(context.Context, workspaceModel, func(context.Context) workspaceSnapshot) (workspaceModel, error) {
		return workspaceModel{}, context.Canceled
	}}
	if err := driveWorkspace(context.Background(), workspaceModel{}, d); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWorkspaceSubmissionDelivery(t *testing.T) {
	r := submissionResult{Attempt: 2, Pass: true, Earned: 2, Max: 2, Checks: []checkJSON{{Name: "check", Pass: true, Message: "done\x1b[2J"}, {Name: "other", Hint: "try again"}}}
	for _, sent := range []bool{false, true} {
		for _, recorded := range []bool{false, true} {
			text := workspaceSubmission(r, sent, recorded)
			if strings.Contains(text, "\x1b") || strings.Contains(text, "Sent to your Astrona account.") != sent || strings.Contains(text, "Saved in local submission history.") != recorded {
				t.Fatal(text)
			}
			if !strings.Contains(text, "PASS  2/2") || !strings.Contains(text, "Hint: try again") {
				t.Fatal(text)
			}
		}
	}
	r.Pass = false
	r.OverTime = true
	if text := workspaceSubmission(r, false, true); !strings.Contains(text, "FAIL") || !strings.Contains(text, "after the exam") {
		t.Fatal(text)
	}
}

func TestWorkspaceNoActionLeavesTaskScreen(t *testing.T) {
	screens := 0
	err := driveWorkspace(t.Context(), workspaceModel{taskTerminals: true}, workspaceDriver{
		screen: func(_ context.Context, m workspaceModel, _ func(context.Context) workspaceSnapshot) (workspaceModel, error) {
			screens++
			if screens > 1 {
				t.Fatal("no-action exit reopened task screen")
			}
			return m, nil
		}, returnHome: func(string) {},
	})
	if err != nil || screens != 1 {
		t.Fatalf("exit: %d %v", screens, err)
	}
}
