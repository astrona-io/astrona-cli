package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/settings"
	"astrona/internal/workspace"

	"github.com/mattn/go-isatty"
)

// wantBrowser decides whether `astrona run` opens the lab in the browser
// workspace. It is asked after trust, before anything is built:
//
//   - --no-browser, --headless and the terminal UI (--ui) never open it;
//   - only Kind labs open in the browser; --browser on another runtime fails;
//   - --browser opens it without asking, even without a terminal;
//   - without a terminal on stdin and stdout the answer is no;
//   - the browser setting answers always or never in advance;
//   - with no browser on this machine (no display) nothing is asked.
//
// The question goes to stderr; always and never are saved to the setting.
func wantBrowser(flags *rootFlags, cfg *config.LabConfig, browser, noBrowser, tui bool) (bool, error) {
	if noBrowser || flags.headless || tui {
		return false, nil
	}
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		if browser {
			return false, fmt.Errorf("--browser opens Kind labs only; this lab runs on %s. Use --ui for the terminal workspace", cfg.Runtime.Type)
		}
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
	switch values.Browser() {
	case settings.BrowserAlways:
		return true, nil
	case settings.BrowserNever:
		return false, nil
	}
	if browserCommand(goruntime.GOOS, os.Getenv, exec.LookPath, "http://127.0.0.1/") == nil {
		return false, nil
	}
	return askBrowser(os.Stdin, promptOut, path)
}

// checkBrowserFlags refuses --browser with a flag that never opens it. It
// runs before anything else; --headless also turns --ui off, so it is
// checked first for a clear message.
func checkBrowserFlags(browser, headless, tui bool) error {
	switch {
	case browser && headless:
		return fmt.Errorf("--browser and --headless cannot be used together")
	case browser && tui:
		return fmt.Errorf("--browser and --ui cannot be used together")
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
	info, _, err := workspace.EnsureServer()
	if err != nil {
		return fmt.Errorf("the lab is running, but the browser workspace did not start: %w", err)
	}
	base, err := info.LaunchURL(workspace.DefaultCodeTTL)
	if err != nil {
		return err
	}
	link := launchWorkspaceURL(base, rec.Lab)
	fmt.Printf("\nBrowser workspace: %s\n", link)
	fmt.Printf("The link works once and for %s; `astrona ui status` prints a new one.\n", workspace.DefaultCodeTTL)
	switch {
	case browserCommand(goruntime.GOOS, os.Getenv, exec.LookPath, link) == nil:
		fmt.Println("No browser to open here: open the link above in a browser on this machine.")
	case !browserOpener(link):
		fmt.Println("Could not open your browser: open the link above.")
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
		Targets:         []workspace.Target{{Name: name, Label: "main", Kind: "kind", Views: []string{"terminal"}}},
	}
	if rec.Exam {
		rec.TimeLimitSeconds = int64(cfg.Exam.Limit() / time.Second)
	}
	links, _ := lifecycle.Links(name)
	for _, link := range links {
		rec.Targets = append(rec.Targets, workspace.Target{Name: link.Cluster, Label: link.Name, Kind: "kind", Views: []string{"terminal"}})
	}
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
