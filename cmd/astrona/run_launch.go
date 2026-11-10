package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	goruntime "runtime"
	"sync"
	"syscall"
	"time"

	"astrona/internal/settings"
	"astrona/internal/terms"
	"astrona/internal/ui"
	"astrona/internal/workspace"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// An interactive `astrona run` that will open the browser opens it first:
// the tab shows the CLI terms (when they still need accepting), then the lab
// being set up, then the lab itself. The learner can leave the terminal as
// soon as the command starts. Questions the terminal still asks (start over?,
// log in?, trust this repository?) are answered there; the page says so.

const (
	// browserTermsWait is how long run waits for an answer to the terms.
	browserTermsWait = 30 * time.Minute
	// launchPageGone is how long without the page asking before run
	// treats the tab as closed and opens a new one for the ready lab.
	launchPageGone = 10 * time.Second
	// launchStatusEvery throttles status-only rewrites of launch.json.
	launchStatusEvery = 500 * time.Millisecond
	// launchKeep is how long launch files are kept before they are pruned.
	launchKeep = 24 * time.Hour
)

// browserFirst reports whether this command is an interactive `astrona run`
// that opens the browser workspace without asking: --browser, or the
// browser setting at always (the default), on a machine with a browser.
// With browser=ask the question comes later in the terminal, as before.
func browserFirst(cmd *cobra.Command, flags *rootFlags) bool {
	if cmd.CommandPath() != cmd.Root().Name()+" run" || commandHeadless(cmd) || terms.InCI(os.Getenv) {
		return false
	}
	if no, _ := cmd.Flags().GetBool("no-browser"); no {
		return false
	}
	for _, f := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		if !isatty.IsTerminal(f.Fd()) {
			return false
		}
	}
	if browserCommand(goruntime.GOOS, os.Getenv, exec.LookPath, "http://127.0.0.1/") == nil {
		return false
	}
	if yes, _ := cmd.Flags().GetBool("browser"); yes {
		return true
	}
	path, err := settings.Path(flags.settingsFile)
	if err != nil {
		return false
	}
	values, err := settings.Load(path)
	if err != nil {
		return false
	}
	pref, _ := values.Browser()
	return pref == settings.BrowserAlways
}

// browserLaunch writes one launch for the page. Every method is safe on a
// nil *browserLaunch (no browser launch) and from several goroutines.
// Writes are best effort: the terminal shows the same progress.
type browserLaunch struct {
	mu        sync.Mutex
	rec       workspace.Launch
	lastWrite time.Time
	failed    bool // a write failed; it was reported once
}

// startBrowserLaunch starts the workspace server (or reuses it), records
// the launch at stage and opens its link in the browser.
func startBrowserLaunch(stage, message string, notice *workspace.LaunchTermsNotice) (*browserLaunch, error) {
	workspace.PruneLaunches(launchKeep)
	id, err := workspace.NewLaunchID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	l := &browserLaunch{rec: workspace.Launch{
		ID: id, PID: os.Getpid(), CreatedAt: now, UpdatedAt: now,
		Stage: stage, Message: message, Terms: notice, Events: []workspace.LaunchEvent{},
	}}
	if err := workspace.WriteLaunch(l.rec); err != nil {
		return nil, err
	}
	// The launch page and its routes are this build's: an older server
	// would show "No lab is running" instead.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	info, _, err := workspace.EnsureCurrentServer(ctx)
	cancel()
	if err != nil {
		_ = workspace.RemoveLaunch(id)
		if errors.Is(err, workspace.ErrOtherBuildInUse) {
			return nil, fmt.Errorf("%w; `astrona ui stop` ends it so the next run shows setup in the browser", err)
		}
		return nil, err
	}
	base, err := info.LaunchURL(workspace.DefaultCodeTTL)
	if err != nil {
		_ = workspace.RemoveLaunch(id)
		return nil, err
	}
	link := base + "&launch=" + id
	fmt.Fprintf(os.Stderr, "Continue in your browser: %s\n", link)
	if !browserOpener(link) {
		fmt.Fprintln(os.Stderr, "Could not open your browser: open the link above (it works once, for 2 minutes).")
	}
	return l, nil
}

func (l *browserLaunch) writeLocked(force bool) {
	if !force && time.Since(l.lastWrite) < launchStatusEvery {
		return
	}
	l.rec.UpdatedAt = time.Now().UTC()
	if err := workspace.WriteLaunch(l.rec); err != nil && !l.failed {
		l.failed = true
		fmt.Fprintf(os.Stderr, "The browser page may stop updating: %v\n", err)
	}
	l.lastWrite = time.Now()
}

// final reports whether the page has nothing more to wait for.
func (l *browserLaunch) finalLocked() bool {
	switch l.rec.Stage {
	case workspace.LaunchReady, workspace.LaunchFailed, workspace.LaunchDeclined, workspace.LaunchEnded:
		return true
	}
	return false
}

// Stage moves the page on to stage with a short message.
func (l *browserLaunch) Stage(stage, message string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finalLocked() {
		return
	}
	l.rec.Stage, l.rec.Message, l.rec.Status = stage, message, ""
	if stage != workspace.LaunchTerms {
		l.rec.Terms = nil
	}
	l.writeLocked(true)
}

// SetTitle names the lab on the page.
func (l *browserLaunch) SetTitle(title string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rec.Title = title
	l.writeLocked(true)
}

// SetAccount tells the page which account the run is saved to, and
// whether the lab is a playground.
func (l *browserLaunch) SetAccount(a *workspace.AccountStatus, playground bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rec.Account = a
	l.rec.Playground = playground
	l.writeLocked(true)
}

// SetLog tells the page where the full run log is.
func (l *browserLaunch) SetLog(path string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rec.LogPath = path
}

// Observe is a ui.Reporter observer: each progress line goes to the page.
func (l *browserLaunch) Observe(e ui.Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finalLocked() {
		return
	}
	if e.Kind == "status" {
		l.rec.Status = e.Detail
		l.writeLocked(false)
		return
	}
	l.rec.Status = ""
	l.rec.Events = append(l.rec.Events, workspace.LaunchEvent{
		At: time.Now().UTC(), Kind: e.Kind, Label: e.Label, Detail: e.Detail, Seconds: e.Elapsed.Seconds(),
	})
	if n := len(l.rec.Events); n > workspace.MaxLaunchEvents {
		l.rec.Events = l.rec.Events[n-workspace.MaxLaunchEvents:]
	}
	l.writeLocked(true)
}

// Ready hands the page the lab to open.
func (l *browserLaunch) Ready(lab string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rec.Stage, l.rec.Lab, l.rec.Status, l.rec.Message = workspace.LaunchReady, lab, "", ""
	l.writeLocked(true)
}

// End stops the page with message when the command ends without a lab
// for it. A launch that already ended keeps its first ending.
func (l *browserLaunch) End(stage, message string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finalLocked() {
		return
	}
	l.rec.Stage, l.rec.Message, l.rec.Status, l.rec.Terms = stage, message, "", nil
	l.writeLocked(true)
}

// finish ends the launch once run returns: an error shows on the page,
// and a run that returns without a lab for the page says so.
func (l *browserLaunch) finish(err error) {
	if l == nil {
		return
	}
	if err != nil {
		l.mu.Lock()
		if !l.finalLocked() {
			l.rec.Error = err.Error()
		}
		l.mu.Unlock()
		l.End(workspace.LaunchFailed, "")
		return
	}
	l.End(workspace.LaunchEnded, "astrona finished in your terminal without opening a lab here.")
}

// pageOpen reports whether the launch's tab asked about it recently.
func (l *browserLaunch) pageOpen() bool {
	if l == nil {
		return false
	}
	seen := workspace.LaunchSeen(l.rec.ID)
	return !seen.IsZero() && time.Since(seen) < launchPageGone
}

// browserTerms shows the CLI terms in a new browser launch and waits for
// the learner to accept or decline them there. Accepting records the
// receipt (method "browser") and the run goes on in the same tab;
// declining, Ctrl+C or no answer stops the command before anything starts.
func browserTerms(cmd *cobra.Command, flags *rootFlags, s terms.Store) error {
	notice := &workspace.LaunchTermsNotice{Revision: terms.Revision, SHA256: terms.Digest(), Text: terms.Document()}
	l, err := startBrowserLaunch(workspace.LaunchTerms, "", notice)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not show the terms in your browser (%v); asking here instead.\n", err)
		return s.Ensure(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), true, false, false, Version)
	}
	flags.launch = l
	fmt.Fprintln(os.Stderr, "Read the Astrona CLI terms in your browser and accept or decline them there. Ctrl+C cancels.")

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := time.NewTimer(browserTermsWait)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		answer, err := workspace.ReadLaunchAnswer(l.rec.ID)
		switch {
		case err == nil && !answer.Accepted:
			l.End(workspace.LaunchDeclined, "")
			return errors.New("CLI terms declined in the browser; nothing was started")
		case err == nil && answer.SHA256 != terms.Digest():
			l.End(workspace.LaunchFailed, "The terms changed while the page was open; run the command again.")
			return errors.New("the accepted terms do not match this CLI's terms; nothing was started")
		case err == nil:
			if err := s.Accept("browser", Version); err != nil {
				l.End(workspace.LaunchFailed, "")
				return err
			}
			fmt.Fprintf(os.Stderr, "Accepted Astrona CLI terms %s in the browser (SHA-256: %s).\n", terms.Revision, terms.Digest())
			l.Stage(workspace.LaunchChecking, "")
			return nil
		case !errors.Is(err, os.ErrNotExist):
			l.End(workspace.LaunchFailed, "")
			return fmt.Errorf("read the answer to the terms: %w", err)
		}
		select {
		case <-ctx.Done():
			l.End(workspace.LaunchEnded, "Cancelled in the terminal; nothing was started.")
			return errors.New("cancelled before the terms were accepted; nothing was started")
		case <-deadline.C:
			l.End(workspace.LaunchEnded, "No answer to the terms; nothing was started. Run the command again when you are ready.")
			return errors.New("no answer to the CLI terms in the browser; nothing was started")
		case <-tick.C:
		}
	}
}
