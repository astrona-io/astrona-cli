package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/proctor"
	"astrona/internal/terms"
	"astrona/internal/ui"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func requireWorkspaceTerminal() error {
	return checkWorkspaceTerminal(isatty.IsTerminal(os.Stdin.Fd()), isatty.IsTerminal(os.Stdout.Fd()), terms.InCI(os.Getenv), os.Getenv("TERM"), os.Getenv(labShellEnvVar))
}

func checkWorkspaceTerminal(input, output, ci bool, term, nested string) error {
	if ci || !input || !output || term == "dumb" {
		return fmt.Errorf("the lab UI needs an interactive terminal (stdin and stdout), outside CI; run without --ui for scripts")
	}
	if nested != "" {
		return fmt.Errorf("already inside a lab shell for %s; exit it before opening the UI", nested)
	}
	return nil
}

func workspaceRuntime(cfg *config.LabConfig) error {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return fmt.Errorf("the lab UI currently supports Kind labs; run without --ui and use astrona ssh for QEMU labs")
	}
	return nil
}

func newWorkspaceCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ui [lab-name]",
		Short: "Open the UI home with active runs and history",
		Long: "Open Home to browse saved runs, submission history and completed labs/playgrounds, even when no lab or engine is running. " +
			"Enter opens a running Kind lab; name one to open its workspace directly. " +
			"Opening the workspace never restarts a lab or its timer. Use astrona run <lab> --ui to start one.\n\n" +
			"Arrow keys select a cluster; s or Enter opens a local shell with that lab's isolated kubeconfigs. " +
			"Exit the shell to return. Tab switches between the task and submission results; Page Up/Down scroll. " +
			"p opens saved preferences: select a setting, Enter to edit and review, then y to save. " +
			"Esc cancels. Defaults and descriptions are shown, and update-check requires an explicit true/false choice. " +
			"Saved options apply to future commands; cache policy is checked after the UI closes. " +
			"g submits a graded lab through the Proctor and returns Home, keeping the lab running, even after a pass. " +
			"q or Ctrl-C opens an exit menu: return Home with the lab running, submit (graded labs), or explicitly confirm destruction and return Home. " +
			"Closing the terminal does not submit or destroy; existing time limits still apply.\n\n" +
			"Requires an interactive terminal outside CI. Opening the UI is opt-in, not a saved setting; QEMU is not yet supported.",
		Example:           "  astrona ui\n  astrona ui " + apphome.ClusterPrefix() + "my-lab\n  astrona run ./labs/my-lab --ui",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: labCompletion(isKind),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireWorkspaceTerminal(); err != nil {
				return err
			}
			if len(args) == 0 {
				return runUIHome(cmd.Context(), flags, "")
			}
			names, err := workspaceLabNames()
			if err != nil {
				return err
			}
			name := config.NormalizeClusterName(args[0])
			if !slices.Contains(names, name) {
				return fmt.Errorf("no saved, running Kind lab named %s; use astrona ui to view saved runs and history", name)
			}
			st, err := labstate.Load(name)
			if err != nil {
				return err
			}
			if st == nil || st.Source == nil {
				return fmt.Errorf("lab %s has no saved config source; use astrona run <config> --ui to attach", name)
			}
			applySource(flags, st.Source)
			cfg, baseDir, cleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer cleanup()
			if config.NormalizeClusterName(cfg.Metadata.Name) != name {
				return fmt.Errorf("saved config now names a different lab; refusing to operate on %s", name)
			}
			return runLabWorkspace(cmd.Context(), cfg, baseDir, flags)
		},
	}
}

// Only remembered primary Kind labs belong in the picker, never an unrelated
// Kubernetes context or a linked cluster that belongs to a different lab.
func workspaceLabNames() ([]string, error) {
	known, err := labstate.Names()
	if err != nil {
		return nil, err
	}
	owners := linkedClusterOwners()
	var names []string
	for _, row := range collectKindRows() {
		if _, linked := owners[row.name]; !linked && isRunning(row) && slices.Contains(known, row.name) && !strings.HasPrefix(row.name, apphome.TestClusterPrefix()) {
			names = append(names, row.name)
		}
	}
	slices.Sort(names)
	return names, nil
}

func workspaceRunID(st *labstate.State) string {
	if st == nil {
		return ""
	}
	session := ""
	if st.Session != nil {
		session = st.Session.ID
	}
	source, _ := json.Marshal(st.Source) // Source contains only strings.
	return st.StartedAt + "\n" + session + "\n" + string(source)
}

func workspaceCanSubmit(cfg *config.LabConfig, playground bool) bool {
	v := cfg.Validation
	return !playground && (len(v.Checks) > 0 || v.Script != nil || len(v.Scripts) > 0)
}

func workspaceGuard(name, runID string) error {
	st, err := labstate.Load(name)
	if err != nil {
		return err
	}
	if workspaceRunID(st) != runID {
		return fmt.Errorf("lab stopped or restarted elsewhere; close and reopen the workspace")
	}
	if st != nil && st.Session.IsPlayground() {
		if deadline, err := time.Parse(time.RFC3339, st.Session.DeadlineAt); err == nil && !time.Now().Before(deadline) {
			return fmt.Errorf("playground time limit reached; its watchdog handles cleanup")
		}
	}
	return nil
}

func runLabWorkspace(ctx context.Context, cfg *config.LabConfig, baseDir string, flags *rootFlags) error {
	notice, err := openLabWorkspace(ctx, cfg, baseDir, flags, "")
	if err != nil {
		return err
	}
	return runUIHome(ctx, flags, notice)
}

func openLabWorkspace(ctx context.Context, cfg *config.LabConfig, baseDir string, flags *rootFlags, expectedRunID string) (string, error) {
	if err := workspaceRuntime(cfg); err != nil {
		return "", err
	}
	name := config.NormalizeClusterName(cfg.Metadata.Name)
	if err := requireRunningKindLab(cfg, name); err != nil {
		return "", err
	}
	st, err := labstate.Load(name)
	if err != nil {
		return "", err
	}
	if st == nil {
		return "", fmt.Errorf("lab %s has no saved run state; restart it before using the workspace", name)
	}
	if expectedRunID != "" && workspaceRunID(st) != expectedRunID {
		return "", fmt.Errorf("run changed while opening %s; select it again from Home", name)
	}
	if !sameSource(st.Source, rememberedSource(flags)) {
		return "", fmt.Errorf("lab %s was started from another config; attach using its saved source with astrona ui %s", name, name)
	}
	runID := workspaceRunID(st)
	links, _ := lifecycle.Links(name)
	clusters := []workspaceCluster{{name: name, status: "Checking…"}}
	for _, link := range links {
		clusters = append(clusters, workspaceCluster{name: link.Cluster, alias: link.Name, status: "Checking…"})
	}
	playground := st.Session.IsPlayground() || isPlaygroundName(flags.catalogLab) || filepath.Base(baseDir) == "playground"
	m := workspaceModel{name: name, task: workspaceTask(cfg, baseDir, flags), canSubmit: workspaceCanSubmit(cfg, playground), snapshot: workspaceSnapshot{clusters: clusters}}
	refresh := func(ctx context.Context) workspaceSnapshot {
		return readWorkspaceSnapshot(ctx, name, runID, clusters, playground, cfg.Exam.Enabled())
	}
	notice := ""
	err = driveWorkspace(ctx, m, workspaceDriver{
		returnHome: func(message string) { notice = message },
		screen:     runWorkspaceScreen, refresh: refresh,
		preferences: func() (workspacePreferences, error) {
			path, err := settingsPath(flags)
			if err != nil {
				return workspacePreferences{}, err
			}
			return loadWorkspacePreferences(path)
		},
		savePreference: func(row workspaceSetting, value string) error {
			path, err := settingsPath(flags)
			if err != nil {
				return err
			}
			return saveWorkspacePreference(ctx, path, row, value)
		},
		guard: func() error { return workspaceGuard(name, runID) },
		shell: func(alias string) error { return runLabShell(name, alias, nil) },
		submit: func() (string, error) {
			result := ""
			err := submitLoadedLab(flags, cfg, baseDir, submitOptions{keep: true, completed: func(r submissionResult, sent, recorded bool) { result = workspaceSubmission(r, sent, recorded) }}, os.Stdout)
			return result, err
		},
		destroy: func() (bool, error) {
			rep, err := ui.NewReporter("destroy", name, flags.verbose)
			if err != nil {
				return false, err
			}
			defer rep.Close()
			info := teardownInfo{clusterName: name, teardown: cfg.Teardown, runtime: cfg.Runtime, cfg: cfg}
			info.skipScripts = !teardownTrusted(flags, info, baseDir, rep)
			err = destroyLab(info, baseDir, rep)
			return err == nil && !cfg.Teardown.KeepCluster, err
		},
	})
	return notice, err
}

// Lifecycle effects are separate from rendering, making the exit and failure
// paths testable without a container engine or a real account.
type workspaceDriver struct {
	returnHome     func(string)
	screen         func(context.Context, workspaceModel, func(context.Context) workspaceSnapshot) (workspaceModel, error)
	refresh        func(context.Context) workspaceSnapshot
	guard          func() error
	shell          func(string) error
	submit         func() (string, error)
	destroy        func() (bool, error)
	preferences    func() (workspacePreferences, error)
	savePreference func(workspaceSetting, string) error
}

func driveWorkspace(ctx context.Context, m workspaceModel, d workspaceDriver) error {
	for {
		var err error
		m, err = d.screen(ctx, m, d.refresh)
		if err != nil {
			return err
		}
		if m.action == workspaceLeave || m.action == workspaceNone {
			if d.returnHome != nil {
				d.returnHome("Left " + m.name + " running. Select it in Active runs to reopen.")
			} else {
				fmt.Printf("Workspace closed; the lab was not destroyed on exit. Reopen: astrona ui %s\n", m.name)
			}
			return nil
		}
		m.exitMenu, m.confirmDestroy = false, false
		// Preferences do not act on the lab and remain usable if it expires.
		// File access happens here, after Bubble Tea releases the terminal.
		if m.action == workspaceLoadPreferences || m.action == workspaceSavePreference {
			m.showPreferences = true
			selected := m.preferences.selected
			if m.action == workspaceSavePreference {
				if selected < 0 || selected >= len(m.preferences.rows) {
					m.preferences.notice = "No setting selected; press r to reload."
					continue
				}
				err = d.savePreference(m.preferences.rows[selected], m.preferences.draft)
				if err != nil {
					m.preferences.editing, m.preferences.confirming = true, false
					m.preferences.notice = "Not saved: " + err.Error()
					continue
				}
			}
			m.preferences, err = d.preferences()
			m.preferences.selected = min(selected, max(0, len(m.preferences.rows)-1))
			if m.action == workspaceSavePreference {
				m.preferences.notice = "Saved. The running lab was not changed."
			}
			if err != nil {
				m.preferences.notice += " Could not load preferences: " + err.Error()
			}
			continue
		}
		if err = d.guard(); err != nil {
			m.notice = err.Error()
			m.snapshot.blocked = err.Error()
			continue
		}
		switch m.action {
		case workspaceShell:
			err = d.shell(m.snapshot.clusters[m.selected].alias)
		case workspaceSubmit:
			if !m.canSubmit {
				continue
			}
			m.submission, err = d.submit()
			m.results, m.scroll = true, 0
			if err != nil && exitCodeFor(err) == 2 {
				err = nil
			}
			if d.returnHome != nil && (err == nil || m.submission != "") {
				message := m.submission + "\nLab " + m.name + " remains running."
				if err != nil {
					message += "\n" + err.Error()
				}
				d.returnHome(message)
				return nil
			}
		case workspaceDestroy:
			var destroyed bool
			destroyed, err = d.destroy()
			if err == nil && destroyed {
				if d.returnHome != nil {
					d.returnHome("Destroyed " + m.name + ". View History for saved runs and results.")
				}
				return nil
			}
			if err == nil {
				m.notice = "The lab's teardown.keepCluster setting kept it running."
			}
		}
		if err != nil {
			m.notice = err.Error()
		} else if m.action != workspaceDestroy {
			m.notice = ""
		}
	}
}

func workspaceSubmission(r submissionResult, sent, recorded bool) string {
	result := "FAIL"
	if r.Pass {
		result = "PASS"
	}
	text := fmt.Sprintf("Attempt #%d  %s  %d/%d points\n", r.Attempt, result, r.Earned, r.Max)
	if recorded {
		text += "Saved in local submission history.\n"
	} else {
		text += "Could not save locally. Keep this result before closing the workspace.\n"
	}
	if sent {
		text += "Sent to your Astrona account.\n"
	} else {
		text += "Not sent to Astrona. Check the submission output for details.\n"
	}
	if r.OverTime {
		text += "Submitted after the exam time limit.\n"
	}
	for _, c := range r.Checks {
		status := "FAIL"
		if c.Pass {
			status = "PASS"
		}
		text += fmt.Sprintf("\n[%s] %s", status, c.Name)
		if c.Message != "" {
			text += "\n" + c.Message
		}
		if c.Hint != "" {
			text += "\nHint: " + c.Hint
		}
	}
	return sanitizeTerminalText(text)
}

func workspaceTask(cfg *config.LabConfig, baseDir string, flags *rootFlags) string {
	if cfg.Metadata.Docs.Question == "" {
		return "No task document provided. Use the shell to explore the lab."
	}
	configPath := filepath.Join(baseDir, flags.fileName)
	if strings.HasPrefix(flags.configPath, "https://") || strings.HasPrefix(flags.configPath, "http://") {
		configPath = flags.configPath
		if !strings.HasSuffix(configPath, ".yaml") && !strings.HasSuffix(configPath, ".yml") {
			configPath = strings.TrimRight(configPath, "/") + "/" + flags.fileName
		}
	}
	text, err := readLabDoc(configPath, cfg.Metadata.Docs.Question)
	if err != nil {
		return "Could not read the task: " + sanitizeLine(err.Error())
	}
	title, body := stripFrontMatter(sanitizeTerminalText(text))
	if title != "" {
		return title + "\n\n" + body
	}
	return body
}

func readWorkspaceSnapshot(ctx context.Context, name, runID string, clusters []workspaceCluster, playground, timed bool) workspaceSnapshot {
	snap := workspaceSnapshot{clusters: append([]workspaceCluster(nil), clusters...), kind: "Lab", tracking: "Local only: no account session", result: "No submissions recorded. Press g to submit a graded lab."}
	if playground {
		snap.kind = "Playground"
		snap.result = "Playgrounds are not graded."
	}
	st, err := labstate.Load(name)
	if err != nil {
		snap.blocked = err.Error()
	} else if workspaceRunID(st) != runID {
		snap.blocked = "Lab stopped or restarted elsewhere; close and reopen the workspace."
	}
	if st != nil {
		snap.started, _ = time.Parse(time.RFC3339, st.StartedAt)
		if st.Session != nil {
			snap.tracking = "Account session: " + st.Session.ID + " (upload checked on submit)"
			if st.Session.IsPlayground() {
				snap.deadline, _ = time.Parse(time.RFC3339, st.Session.DeadlineAt)
			}
		}
	}
	if timed {
		if clock, err := exam.Load(name); err == nil && clock != nil {
			snap.started = clock.StartedAt
			snap.deadline = clock.StartedAt.Add(clock.Limit)
		}
	}
	if playground && !snap.deadline.IsZero() && !time.Now().Before(snap.deadline) {
		snap.blocked = "Playground time limit reached; its watchdog handles cleanup."
	}
	if !playground {
		if attempts, err := proctor.LoadAttempts(name); err != nil {
			snap.result = "Could not load submissions: " + err.Error()
		} else if len(attempts) > 0 {
			a := attempts[len(attempts)-1]
			result := "FAIL"
			if a.Pass {
				result = "PASS"
			}
			snap.result = fmt.Sprintf("Attempt #%d  %s  %d/%d points\n%s\n", len(attempts), result, a.Earned, a.Max, a.Time.Local().Format(time.RFC1123))
			for _, check := range a.Checks {
				status := "FAIL"
				if check.Pass {
					status = "PASS"
				}
				snap.result += fmt.Sprintf("\n[%s] %s", status, check.Name)
			}
		}
	}
	// A bounded worker pool keeps a lab with many linked clusters responsive.
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range min(4, len(snap.clusters)) {
		wg.Go(func() {
			for i := range jobs {
				snap.clusters[i].status = workspaceClusterHealth(ctx, snap.clusters[i].name)
			}
		})
	}
	for i := range snap.clusters {
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return snap
}

func workspaceClusterHealth(ctx context.Context, name string) string {
	path := cluster.ExistingKubeconfig(name)
	if path == "" {
		return "No isolated kubeconfig"
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", path, "--context", "kind-"+name, "--request-timeout=3s", "get", "nodes", "-o", "json")
	var output workspaceOutput
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return "Unavailable (API/engine)"
	}
	status, _ := nodeHealth(output.Bytes())
	return status
}

// An unhealthy or modified cluster must not fill host memory through polling.
type workspaceOutput struct{ buffer bytes.Buffer }

func (b *workspaceOutput) Bytes() []byte { return b.buffer.Bytes() }
func (b *workspaceOutput) Len() int      { return b.buffer.Len() }

func (b *workspaceOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		return 0, io.ErrShortBuffer
	}
	return b.buffer.Write(p)
}
