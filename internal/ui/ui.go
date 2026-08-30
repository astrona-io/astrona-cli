// Package ui renders the progress of a lifecycle command (run, test,
// submit, destroy) as a compact, per-phase step view: a live spinner while
// a step runs, resolved to a check / cross / dash when it finishes.
//
// Every step's raw subprocess output is always tee'd to a per-run log file
// under ~/.astrona/logs, regardless of mode. In the default (quiet) mode
// that raw output never touches the terminal unless the step fails, in
// which case the tail of it is printed just before the error. With
// verbose set, the spinner is disabled and all raw output streams live to
// stdout as it always did.
package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/theckman/yacspin"
)

// ANSI helpers, no-ops when the screen isn't a color-capable TTY.
func (r *Reporter) bold(s string) string   { return r.wrap("\033[1m", s) }
func (r *Reporter) dim(s string) string    { return r.wrap("\033[2m", s) }
func (r *Reporter) green(s string) string  { return r.wrap("\033[32m", s) }
func (r *Reporter) yellow(s string) string { return r.wrap("\033[33m", s) }
func (r *Reporter) red(s string) string    { return r.wrap("\033[31m", s) }

func (r *Reporter) wrap(code, s string) string {
	if !r.color {
		return s
	}
	return code + s + "\033[0m"
}

// screenIsColorTTY reports whether w is an interactive terminal that should
// receive ANSI styling. Honors the NO_COLOR convention.
func screenIsColorTTY(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// maxCapturedBytes bounds the per-step in-memory capture buffer that a
// failed step prints from. The full, untruncated output is always in the
// log file — this cap only stops a runaway `cat somehugefile` inside a
// bootstrap script from ballooning memory before we'd ever show it.
const maxCapturedBytes = 64 * 1024

// Reporter renders lifecycle progress to a terminal and mirrors everything
// to a run log file. It is not safe for concurrent use by multiple
// goroutines; lifecycle commands drive it from a single goroutine, one
// step at a time.
type Reporter struct {
	verbose bool
	color   bool      // screen is a color-capable TTY
	plain   bool      // no animated spinner: one static line per step
	screen  io.Writer // status lines / spinner target (default os.Stderr)
	raw     io.Writer // live subprocess passthrough in verbose mode (default os.Stdout)
	logW    io.Writer // run log sink
	logC    io.Closer // set when logW is a file we opened
	logPath string

	spin   *yacspin.Spinner // nil in verbose mode
	active *Task
}

// Discard returns a Reporter that renders nothing and keeps no log — for
// tests and any call path that just needs a non-nil *Reporter to satisfy a
// signature.
func Discard() *Reporter {
	return &Reporter{verbose: true, plain: true, screen: io.Discard, raw: io.Discard, logW: io.Discard}
}

// Options configures New. The zero value is valid: non-verbose, status to
// os.Stderr, log path derived under ~/.astrona/logs.
type Options struct {
	Verbose bool
	// Screen is where the spinner and status lines are written. Defaults
	// to os.Stderr when nil.
	Screen io.Writer
	// LogPath overrides the run log file location. Defaults to
	// ~/.astrona/logs/<cmdName>-<labName>-<UTC timestamp>.log when empty.
	LogPath string
}

// NewReporter is the common-case constructor: a reporter for cmdName
// ("run", "test", ...) acting on labName, with the spinner shown unless
// verbose is set.
func NewReporter(cmdName, labName string, verbose bool) (*Reporter, error) {
	return New(cmdName, labName, Options{Verbose: verbose})
}

// New builds a Reporter, opening (creating ~/.astrona/logs as needed) the
// run log file and writing a short header to it. The caller must defer
// Close.
func New(cmdName, labName string, opts Options) (*Reporter, error) {
	screen := opts.Screen
	if screen == nil {
		screen = os.Stderr
	}

	logPath := opts.LogPath
	if logPath == "" {
		dir, err := logDir()
		if err != nil {
			return nil, fmt.Errorf("resolve log directory: %w", err)
		}
		stamp := time.Now().UTC().Format("20060102T150405Z")
		logPath = filepath.Join(dir, fmt.Sprintf("%s-%s-%s.log", sanitize(cmdName), sanitize(labName), stamp))
	} else if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", logPath, err)
	}
	fmt.Fprintf(f, "# astrona %s  lab=%s  %s\n# %s\n\n",
		cmdName, labName, time.Now().UTC().Format(time.RFC3339), strings.Join(os.Args, " "))

	r := &Reporter{
		verbose: opts.Verbose,
		color:   screenIsColorTTY(screen),
		screen:  screen,
		raw:     os.Stdout,
		logW:    f,
		logC:    f,
		logPath: logPath,
	}
	// The animated spinner is only worth it on an interactive terminal.
	// Everywhere else (piped, redirected, CI, verbose) fall back to plain
	// one-line-per-step output.
	r.plain = opts.Verbose || !r.color

	if !r.plain {
		spin, err := yacspin.New(yacspin.Config{
			Frequency: 100 * time.Millisecond,
			Writer:    screen,
			// Keep the cursor visible: if the process dies mid-spin without
			// a Stop/StopFail, a hidden cursor leaves the user's terminal
			// needing a manual `reset`.
			ShowCursor:        true,
			CharSet:           yacspin.CharSets[14],
			Prefix:            "  ",
			Suffix:            " ",
			Colors:            []string{"fgCyan"},
			StopCharacter:     "✓",
			StopColors:        []string{"fgGreen"},
			StopFailCharacter: "✗",
			StopFailColors:    []string{"fgRed"},
		})
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("init spinner: %w", err)
		}
		r.spin = spin
	}

	return r, nil
}

// LogPath returns the path of this run's log file.
func (r *Reporter) LogPath() string { return r.logPath }

// Close stops any running spinner and closes the log file. Safe to call
// once; intended for defer.
func (r *Reporter) Close() error {
	if r.active != nil {
		// A step was left unresolved (early return without Done/Fail) —
		// don't leave the spinner spinning forever.
		r.active.Fail(fmt.Errorf("interrupted"))
	}
	if r.spin != nil && r.spin.Status() != yacspin.SpinnerStopped {
		_ = r.spin.Stop()
	}
	if r.logC != nil {
		err := r.logC.Close()
		r.logC = nil
		r.logW = io.Discard
		return err
	}
	return nil
}

// Section prints a group header ("Bootstrap", "Machine: router").
func (r *Reporter) Section(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	fmt.Fprintf(r.logW, "\n=== %s ===\n", line)
	r.pauseSpinner()
	if r.verbose {
		fmt.Fprintf(r.screen, "\n==> %s\n", line)
	} else {
		fmt.Fprintf(r.screen, "\n%s\n", r.bold(line))
	}
	r.unpauseSpinner()
}

// Info prints a low-priority progress line: shown on screen only in
// verbose mode, but always written to the log.
func (r *Reporter) Info(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	fmt.Fprintf(r.logW, "%s\n", line)
	if r.verbose {
		fmt.Fprintf(r.screen, "%s\n", line)
	}
}

// Warn prints a warning: always shown on screen (above the active spinner
// line) and written to the log.
func (r *Reporter) Warn(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	fmt.Fprintf(r.logW, "[WARN] %s\n", line)
	r.pauseSpinner()
	fmt.Fprintf(r.screen, "%s %s\n", r.yellow("[WARN]"), line)
	r.unpauseSpinner()
}

// Step begins a step and returns its handle. Exactly one step may be
// active at a time; the caller must resolve it with Done, Skip, or Fail
// before starting the next.
func (r *Reporter) Step(format string, a ...any) *Task {
	label := fmt.Sprintf(format, a...)
	if r.active != nil {
		// Defensive: auto-resolve a forgotten step rather than panic.
		r.active.Done()
	}

	fmt.Fprintf(r.logW, "\n--- %s ---\n", label)

	t := &Task{r: r, label: label, start: time.Now()}
	switch {
	case r.verbose:
		t.out = io.MultiWriter(r.raw, r.logW)
		fmt.Fprintf(r.screen, "==> %s\n", label)
	case r.plain:
		t.capture = &cappedBuffer{max: maxCapturedBytes}
		t.out = io.MultiWriter(r.logW, t.capture)
	default:
		t.capture = &cappedBuffer{max: maxCapturedBytes}
		t.out = io.MultiWriter(r.logW, t.capture)
		r.spin.Message(label)
		// Start returns an error only if already running; between steps it
		// is always stopped.
		_ = r.spin.Start()
	}

	r.active = t
	return t
}

func (r *Reporter) pauseSpinner() {
	if r.spin != nil && r.spin.Status() == yacspin.SpinnerRunning {
		_ = r.spin.Pause()
	}
}

func (r *Reporter) unpauseSpinner() {
	if r.spin != nil && r.spin.Status() == yacspin.SpinnerPaused {
		_ = r.spin.Unpause()
	}
}

// Task is a single in-progress step. Resolve it with Done, Skip, or Fail.
type Task struct {
	r       *Reporter
	label   string
	start   time.Time
	out     io.Writer
	capture *cappedBuffer // nil in verbose mode
	done    bool
}

// Output is the writer a step's subprocess stdout and stderr must both be
// wired to. Assign it once and use the same value for both, so os/exec
// serializes the two streams onto one writer:
//
//	out := task.Output()
//	cmd.Stdout = out
//	cmd.Stderr = out
func (t *Task) Output() io.Writer { return t.out }

// Done resolves the step as succeeded.
func (t *Task) Done() {
	if t.done {
		return
	}
	t.done = true
	t.r.active = nil
	elapsed := formatElapsed(time.Since(t.start))
	fmt.Fprintf(t.r.logW, "--- ok: %s (%s) ---\n", t.label, elapsed)
	switch {
	case t.r.verbose:
		fmt.Fprintf(t.r.screen, "    ok: %s (%s)\n", t.label, elapsed)
	case t.r.plain:
		fmt.Fprintf(t.r.screen, "  %s %s %s\n", t.r.green("✓"), t.label, t.r.dim("("+elapsed+")"))
	default:
		t.r.spin.StopMessage(fmt.Sprintf("%s %s", t.label, t.r.dim("("+elapsed+")")))
		_ = t.r.spin.Stop()
	}
}

// Skip resolves the step as not needed, with a short reason.
func (t *Task) Skip(format string, a ...any) {
	if t.done {
		return
	}
	t.done = true
	t.r.active = nil
	reason := fmt.Sprintf(format, a...)
	fmt.Fprintf(t.r.logW, "--- skip: %s (%s) ---\n", t.label, reason)
	switch {
	case t.r.verbose:
		fmt.Fprintf(t.r.screen, "    skip: %s (%s)\n", t.label, reason)
	case t.r.plain:
		fmt.Fprintf(t.r.screen, "  %s %s %s\n", t.r.yellow("-"), t.label, t.r.dim("— "+reason))
	default:
		t.r.spin.StopCharacter("-")
		t.r.spin.StopColors("fgYellow")
		t.r.spin.StopMessage(fmt.Sprintf("%s %s", t.label, t.r.dim("— "+reason)))
		_ = t.r.spin.Stop()
		// Restore the default stop styling for subsequent steps.
		t.r.spin.StopCharacter("✓")
		t.r.spin.StopColors("fgGreen")
	}
}

// Fail resolves the step as failed. Outside verbose mode it prints the
// tail of the step's captured output followed by a pointer to the full
// log. The passed error is returned unchanged for a convenient
// `return t.Fail(err)`.
func (t *Task) Fail(err error) error {
	if t.done {
		return err
	}
	t.done = true
	t.r.active = nil
	elapsed := formatElapsed(time.Since(t.start))
	fmt.Fprintf(t.r.logW, "--- FAIL: %s (%s): %v ---\n", t.label, elapsed, err)

	switch {
	case t.r.verbose:
		fmt.Fprintf(t.r.screen, "    FAIL: %s (%s): %v\n", t.label, elapsed, err)
		return err
	case t.r.plain:
		fmt.Fprintf(t.r.screen, "  %s %s %s\n", t.r.red("✗"), t.label, t.r.dim("("+elapsed+")"))
	default:
		t.r.spin.StopFailMessage(fmt.Sprintf("%s %s", t.label, t.r.dim("("+elapsed+")")))
		_ = t.r.spin.StopFail()
	}

	t.dumpCapture()
	fmt.Fprintf(t.r.screen, "    error: %v\n", err)
	fmt.Fprintf(t.r.screen, "%s\n", t.r.dim("    full log: "+t.r.logPath))
	return err
}

// dumpCapture prints the step's captured subprocess output, indented,
// between rules. No-op when there's nothing captured.
func (t *Task) dumpCapture() {
	if t.capture == nil {
		return
	}
	out := strings.TrimRight(t.capture.String(), "\n")
	if out == "" {
		return
	}
	fmt.Fprintf(t.r.screen, "%s\n", t.r.dim("    ---- output ----"))
	for l := range strings.SplitSeq(out, "\n") {
		fmt.Fprintf(t.r.screen, "    %s\n", l)
	}
	fmt.Fprintf(t.r.screen, "%s\n", t.r.dim("    ----------------"))
}

// cappedBuffer keeps at most max bytes, discarding oldest data first. It
// is safe for the single-writer, single-reader use os/exec + Fail make of
// it, but guarded with a mutex anyway since exec's writer goroutine and
// the reporter goroutine are formally distinct.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, p...)
	if len(c.buf) > c.max {
		c.buf = c.buf[len(c.buf)-c.max:]
		c.truncated = true
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.truncated {
		return "[...truncated, see full log...]\n" + string(c.buf)
	}
	return string(c.buf)
}

// logDir returns (creating if needed) ~/.astrona/logs, mirroring
// hypervisor.QEMUBaseDir / hypervisor.ImageCacheDir.
func logDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".astrona", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create %s: %w", dir, err)
	}
	return dir, nil
}

// sanitize makes s safe for a single path segment in a log filename.
func sanitize(s string) string {
	if s == "" {
		return "-"
	}
	repl := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}
	return strings.Map(repl, s)
}

// formatElapsed renders a step duration compactly: "0.4s", "12s", "3m04s".
func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}
