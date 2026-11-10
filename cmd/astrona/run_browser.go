package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/settings"
	"astrona/internal/ui"
	"astrona/internal/workspace"

	"github.com/mattn/go-isatty"
)

// wantBrowser decides whether `astrona run` opens the lab in the browser
// workspace. It is asked after trust, before anything is built:
//
//   - --no-browser and --headless never open it;
//   - --browser opens it without asking, even without a terminal;
//   - without a terminal on stdin and stdout the answer is no;
//   - the browser setting answers always (the default) or never in
//     advance, or asks;
//   - with no browser on this machine (no display) nothing is asked, and
//     the default does not open the workspace; a saved always still
//     starts it and prints its link.
//
// The question goes to stderr; always and never are saved to the setting.
func wantBrowser(flags *rootFlags, browser, noBrowser bool) (bool, error) {
	if noBrowser || flags.headless {
		return false, nil
	}
	if browser {
		return true, nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd()) {
		return false, nil
	}
	path, err := settings.Path(flags.settingsFile)
	if err != nil {
		return false, err
	}
	values, err := settings.Load(path)
	if err != nil {
		return false, err
	}
	pref, saved := values.Browser()
	hasBrowser := browserCommand(goruntime.GOOS, os.Getenv, exec.LookPath, "http://127.0.0.1/") != nil
	switch pref {
	case settings.BrowserAlways:
		return saved || hasBrowser, nil
	case settings.BrowserNever:
		return false, nil
	}
	if !hasBrowser {
		return false, nil
	}
	return askBrowser(os.Stdin, promptOut, path)
}

// checkBrowserFlags refuses --browser with --headless, which never opens
// it. It runs before anything else.
func checkBrowserFlags(browser, headless bool) error {
	if browser && headless {
		return fmt.Errorf("--browser and --headless cannot be used together")
	}
	return nil
}

// askBrowser asks "Open this lab in your browser?" until it gets yes, no,
// always or never; always and never are saved as the browser setting.
func askBrowser(in io.Reader, out io.Writer, settingsPath string) (bool, error) {
	for {
		fmt.Fprint(out, "Open this lab in your browser? (yes/no/always/never): ")
		answer, err := readAnswerLine(in)
		if err != nil {
			return false, fmt.Errorf("browser choice interrupted; no lab was started: %w", err)
		}
		switch answer {
		case "yes", "y":
			return true, nil
		case "no", "n":
			return false, nil
		case settings.BrowserAlways, settings.BrowserNever:
			value := answer
			if err := settings.Change(settingsPath, "browser", &value); err != nil {
				fmt.Fprintf(out, "Could not save your choice (%v); asking again next time.\n", err)
			} else {
				fmt.Fprintf(out, "Saved browser=%s. Change it with: astrona settings set browser ask\n", value)
			}
			return answer == settings.BrowserAlways, nil
		default:
			fmt.Fprintln(out, "Please enter yes, no, always or never.")
		}
	}
}

// readAnswerLine reads one line a byte at a time, so input meant for a
// later prompt is not buffered away.
func readAnswerLine(in io.Reader) (string, error) {
	var answer strings.Builder
	var b [1]byte
	for answer.Len() < 128 {
		if _, err := io.ReadFull(in, b[:]); err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return strings.ToLower(strings.TrimSpace(answer.String())), nil
		}
		answer.WriteByte(b[0])
	}
	return "", fmt.Errorf("answer is too long")
}

// openBrowserWorkspace runs once the lab is ready: it records the run for
// the workspace server, starts the server (or reuses a running one), prints
// a launch URL with a fresh single-use code and opens it in the browser.
func openBrowserWorkspace(cfg *config.LabConfig, baseDir string, flags *rootFlags) error {
	rec, err := browserRunRecord(cfg, baseDir, flags)
	if err != nil {
		return fmt.Errorf("the lab is running, but the browser workspace could not be prepared: %w", err)
	}
	if err := workspace.WriteRun(rec); err != nil {
		return fmt.Errorf("the lab is running, but the browser workspace could not be prepared: %w", err)
	}
	if flags.launch != nil {
		// The tab that followed the setup opens the lab itself.
		flags.launch.Ready(rec.Lab)
		if flags.launch.pageOpen() {
			fmt.Printf("\nThe lab is ready in your browser: click Begin this session. Closed the tab? `astrona ui %s` opens it again.\n", uiLabArg(rec.Lab))
			printRunsInBackground(rec.Lab)
			return nil
		}
	}
	if err := launchBrowserWorkspace(rec.Lab); err != nil {
		return err
	}
	printRunsInBackground(rec.Lab)
	return nil
}

// printRunsInBackground says that the lab outlives this command, so a
// learner does not leave labs running without knowing it.
func printRunsInBackground(lab string) {
	fmt.Printf("It keeps running in the background until you end it (astrona destroy %s). "+
		"`astrona ui` shows every lab running on this computer.\n", uiLabArg(lab))
}

// reopenBrowserWorkspace is astrona ui <lab>: it keeps the run's record
// when it is the current run (so a Start already clicked stays), else
// records it. A lab whose exam clock already runs (started without the
// browser) is recorded as started, so the page can never restart its clock.
func reopenBrowserWorkspace(cfg *config.LabConfig, baseDir string, flags *rootFlags) error {
	rec, err := browserRunRecord(cfg, baseDir, flags)
	if err != nil {
		return fmt.Errorf("the browser workspace could not be prepared: %w", err)
	}
	if saved, err := workspace.ReadRun(rec.Lab); err != nil || saved.RunID != rec.RunID {
		if err := workspace.WriteRun(rec); err != nil {
			return fmt.Errorf("the browser workspace could not be prepared: %w", err)
		}
		clock, err := exam.Load(rec.Lab)
		if err != nil {
			return err
		}
		if rec.Exam && clock != nil {
			if err := workspace.MarkStarted(rec.Lab, clock.StartedAt); err != nil {
				return fmt.Errorf("the browser workspace could not be prepared: %w", err)
			}
			reportClockStarted(rec.Lab, clock.StartedAt)
		}
	}
	return launchBrowserWorkspace(rec.Lab)
}

// reportClockStarted tells Astrona that lab's clock started at at (at
// readiness, outside the browser), as the workspace's Start does: the site
// keeps the first start, so one the lab page already set stays. It waits a
// few seconds at most; offline the start stays in the outbox and a detached
// flusher sends it. A local-only or untracked run sends nothing.
func reportClockStarted(lab string, at time.Time) {
	sess, err := labops.StartTarget(lab)
	if err != nil || sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	outcome, _, err := labops.SignalStart(ctx, sess, at)
	switch {
	case err != nil:
		ui.Warnf("the lab's start was not sent to %s: %s", sess.Site, err)
	case outcome == labops.ClockWaiting:
		ui.Warnf("the lab's start couldn't reach %s — it is saved on this machine and sent when Astrona is reachable", sess.Site)
	}
}

// launchBrowserWorkspace starts the workspace server (or reuses a running
// one), prints a launch URL for lab with a fresh single-use code and opens
// it in the browser, or says to open it when there is no browser here.
func launchBrowserWorkspace(lab string) error {
	info, _, err := workspace.EnsureServer()
	if err != nil {
		return fmt.Errorf("the lab is running, but the browser workspace did not start: %w", err)
	}
	base, err := info.LaunchURL(workspace.DefaultCodeTTL)
	if err != nil {
		return err
	}
	link := launchWorkspaceURL(base, lab)
	fmt.Printf("\nBrowser workspace: %s\n", link)
	fmt.Printf("The link works once and for %s; `astrona ui %s` opens a new one.\n", workspace.DefaultCodeTTL, uiLabArg(lab))
	switch {
	case browserCommand(goruntime.GOOS, os.Getenv, exec.LookPath, link) == nil:
		fmt.Println("No browser to open here: open the link above in a browser on this machine.")
	case !browserOpener(link):
		fmt.Println("Could not open your browser: open the link above.")
	}
	return nil
}

// openWorkspaceHome opens "Labs on this computer" in the browser workspace:
// every lab and playground still running here, with open and end actions.
func openWorkspaceHome(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	info, _, err := workspace.EnsureCurrentServer(ctx)
	cancel()
	if errors.Is(err, workspace.ErrOtherBuildInUse) {
		// An older server with a page open still serves its own page.
		info, _, err = workspace.EnsureServer()
	}
	if err != nil {
		return fmt.Errorf("the browser workspace did not start: %w", err)
	}
	base, err := info.LaunchURL(workspace.DefaultCodeTTL)
	if err != nil {
		return err
	}
	link := base
	fmt.Fprintf(out, "\nLabs on this computer: %s\n", link)
	fmt.Fprintf(out, "The link works once and for %s; astrona ui opens a new one.\n", workspace.DefaultCodeTTL)
	switch {
	case browserCommand(goruntime.GOOS, os.Getenv, exec.LookPath, link) == nil:
		fmt.Fprintln(out, "No browser to open here: open the link above in a browser on this machine.")
	case !browserOpener(link):
		fmt.Fprintln(out, "Could not open your browser: open the link above.")
	}
	return nil
}

// browserRunRecord is what the page may show about this run: titles, the
// task, prerequisites, rules and terminal targets. Never the solution,
// validation, credentials or kubeconfigs.
func browserRunRecord(cfg *config.LabConfig, baseDir string, flags *rootFlags) (workspace.RunRecord, error) {
	name := config.NormalizeClusterName(cfg.Metadata.Name)
	st, err := labstate.Load(name)
	if err != nil {
		return workspace.RunRecord{}, err
	}
	if st == nil {
		return workspace.RunRecord{}, fmt.Errorf("lab %s has no saved state", name)
	}
	rec := workspace.RunRecord{
		Lab:             name,
		RunID:           workspace.RunDigest(labops.RunID(st)),
		Title:           labops.SanitizeLine(cfg.Metadata.Name),
		Exam:            cfg.Exam.Enabled(),
		Playground:      st.Session.IsPlayground() || labops.IsPlaygroundName(flags.catalogLab) || filepath.Base(baseDir) == "playground",
		Strict:          cfg.Exam.Strict,
		HintsHidden:     cfg.Exam.HideHints,
		KeepEnvironment: cfg.Teardown.KeepCluster,
		Targets:         browserTargets(cfg, name),
		Account:         flags.runAccount,
	}
	if st.Source != nil {
		rec.RunAgain = labops.SourceCommand("run", st.Source)
	}
	if rec.Exam {
		rec.TimeLimitSeconds = int64(cfg.Exam.Limit() / time.Second)
	}
	var links []cluster.LinkState
	if !isQEMULab(cfg) {
		links, _ = lifecycle.Links(name)
	}
	for _, link := range links {
		rec.Targets = append(rec.Targets, workspace.Target{Name: link.Cluster, Label: link.Name, Kind: workspace.TargetKind, Views: []string{"terminal"}})
	}
	tabs, err := browserTabs(cfg, rec.Targets, links, baseDir, flags)
	if err != nil {
		return workspace.RunRecord{}, err
	}
	rec.Tabs = tabs
	rec.QuestionTitle = "Task"
	rec.Question = "No task document provided. Use the terminal to explore the lab."
	if cfg.Metadata.Docs.Question != "" {
		text, err := readLabDocument(cfg.Metadata.Docs.Question, baseDir, flags)
		if err != nil {
			return workspace.RunRecord{}, fmt.Errorf("read the task: %w", err)
		}
		meta, body := docFrontMatter(labops.SanitizeTerminalText(text))
		if title, ok := meta["title"].(string); ok && strings.TrimSpace(title) != "" {
			rec.Title = labops.SanitizeLine(strings.TrimSpace(title))
			rec.QuestionTitle = rec.Title
		}
		if estimate, ok := meta["estimated_duration"].(string); ok {
			if d, err := time.ParseDuration(strings.TrimSpace(estimate)); err == nil && d > 0 {
				rec.EstimatedDuration = strings.TrimSpace(estimate)
			}
		}
		rec.Question = body
	}
	if doc := cfg.Metadata.Docs.Prerequisites; doc != "" && !rec.Playground {
		text, err := readLabDocument(doc, baseDir, flags)
		if err != nil {
			return workspace.RunRecord{}, fmt.Errorf("read the prerequisites: %w", err)
		}
		_, rec.Prerequisites = docFrontMatter(labops.SanitizeTerminalText(text))
	}
	return rec, nil
}

// isQEMULab reports whether cfg runs on QEMU VMs rather than Kind.
func isQEMULab(cfg *config.LabConfig) bool { return cfg.Runtime.Type == "qemu" }

// browserTargets are the places the page opens terminals on, the lab's own
// first: its Kind cluster, or each of its VMs by the name astrona ssh takes
// (the lab itself for a single VM, <lab>-<vm> for each VM of a multi-VM
// lab, labelled with the VM's name). Linked clusters are added by the caller.
func browserTargets(cfg *config.LabConfig, name string) []workspace.Target {
	if !isQEMULab(cfg) {
		return []workspace.Target{{Name: name, Label: "main", Kind: workspace.TargetKind, Views: []string{"terminal"}}}
	}
	if !config.IsMultiVM(cfg.Runtime.QEMU) {
		return []workspace.Target{{Name: name, Label: "main", Kind: workspace.TargetVM, Views: []string{"terminal"}}}
	}
	targets := make([]workspace.Target, 0, len(cfg.Runtime.QEMU))
	for _, vm := range cfg.Runtime.QEMU {
		targets = append(targets, workspace.Target{Name: name + "-" + vm.Name, Label: vm.Name, Kind: workspace.TargetVM, Views: []string{"terminal"}})
	}
	return targets
}

// readLabDocument reads one of the lab's docs (a path from metadata.docs),
// next to its config file or URL.
func readLabDocument(doc, baseDir string, flags *rootFlags) (string, error) {
	configPath := filepath.Join(baseDir, flags.fileName)
	if strings.HasPrefix(flags.configPath, "https://") || strings.HasPrefix(flags.configPath, "http://") {
		configPath = flags.configPath
		if !strings.HasSuffix(configPath, ".yaml") && !strings.HasSuffix(configPath, ".yml") {
			configPath = strings.TrimRight(configPath, "/") + "/" + flags.fileName
		}
	}
	return labops.ReadLabDoc(configPath, doc)
}

// browserTabs are the right panel's tabs: the focus tab (the lab's own
// cluster), then the lab's tabs. Only what the lab declares becomes a tab;
// without `tabs` there is only the focus tab.
func browserTabs(cfg *config.LabConfig, targets []workspace.Target, links []cluster.LinkState, baseDir string, flags *rootFlags) ([]workspace.Tab, error) {
	if len(cfg.Tabs) == 0 {
		return workspace.DefaultTabs(targets), nil
	}
	if err := config.ValidateTabs(cfg); err != nil {
		return nil, err
	}
	forwards := map[string]config.PortForward{}
	for _, pf := range cfg.Runtime.PortForwards {
		forwards[pf.Name] = pf.Normalized()
	}
	tabs := []workspace.Tab{workspace.FocusTab(targets[0].Name)}
	for _, t := range cfg.Tabs {
		tab := workspace.Tab{Name: t.Name, Title: labops.SanitizeLine(strings.TrimSpace(t.Title)), Kind: t.Type}
		switch t.Type {
		case config.TabTerminal:
			tab.Target = targets[0].Name
			if t.Cluster != "" {
				tab.Target = ""
				for _, link := range links {
					if link.Name == t.Cluster {
						tab.Target = link.Cluster
					}
				}
				if tab.Target == "" {
					return nil, fmt.Errorf("tab %s: linked cluster %s is not running", t.Name, t.Cluster)
				}
			}
		case config.TabBrowser:
			pf := forwards[t.PortForward]
			path, err := config.TabPath(t.Path)
			if err != nil {
				return nil, fmt.Errorf("tab %s: %w", t.Name, err)
			}
			tab.URL = workspace.BrowserTabURL(pf.Scheme, pf.HostPort, path)
		case config.TabDocs:
			if t.URL != "" {
				tab.URL = t.URL
				break
			}
			text, err := readLabDocument(t.File, baseDir, flags)
			if err != nil {
				return nil, fmt.Errorf("read the document of tab %s: %w", t.Name, err)
			}
			if len(text) > config.MaxTabDocBytes {
				return nil, fmt.Errorf("the document of tab %s is larger than %d KiB", t.Name, config.MaxTabDocBytes>>10)
			}
			_, tab.Doc = docFrontMatter(labops.SanitizeTerminalText(text))
		}
		tabs = append(tabs, tab)
	}
	return tabs, nil
}
