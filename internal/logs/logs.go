// Package logs discovers, parses, and prunes the per-run log files that
// internal/ui writes under ~/.astrona/logs. Every lifecycle command (run,
// test, submit, destroy) tees its full output to one such file; this
// package is the read side that `astrona logs` is built on.
//
// A log file is named "<command>-<lab>-<UTC stamp>.log" and starts with a
// two-line header written by ui.New:
//
//	# astrona run  lab=demo  2026-08-30T12:00:00Z
//	# astrona run --config ./lab
//
// and (for runs whose Reporter.Close ran) ends with a one-line footer:
//
//	# done: result=ok steps=4/0/1 duration=1m03s
//
// The header is authoritative for command/lab/start-time; the filename is
// only a fallback when the header is missing or malformed.
package logs

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// stampLayout is the UTC timestamp format ui.New puts in a log filename.
const stampLayout = "20060102T150405Z"

// maxScanLine bounds a single line read while scanning a log for its
// header/footer/step tallies — a bootstrap script that `cat`s a big file
// can produce very long lines, and bufio.Scanner's 64 KiB default would
// error out on them.
const maxScanLine = 4 * 1024 * 1024

var (
	headerRe = regexp.MustCompile(`^# astrona (\S+)\s+lab=(.+?)\s+(\S+)\s*$`)
	footerRe = regexp.MustCompile(`^# done: result=(\S+) steps=(\d+)/(\d+)/(\d+) duration=(\S+)\s*$`)
)

// Entry is the metadata of one run log, as shown by `astrona logs list`.
type Entry struct {
	Index    int       `json:"index" yaml:"index"`
	Path     string    `json:"path" yaml:"path"`
	File     string    `json:"file" yaml:"file"`
	Command  string    `json:"command" yaml:"command"`
	Lab      string    `json:"lab" yaml:"lab"`
	Started  time.Time `json:"started" yaml:"started"`
	Duration string    `json:"duration,omitempty" yaml:"duration,omitempty"`
	Size     int64     `json:"size_bytes" yaml:"size_bytes"`
	Result   string    `json:"result" yaml:"result"`
	StepsOK  int       `json:"steps_ok" yaml:"steps_ok"`
	StepsSkp int       `json:"steps_skipped" yaml:"steps_skipped"`
	StepsErr int       `json:"steps_failed" yaml:"steps_failed"`
	Argv     string    `json:"argv,omitempty" yaml:"argv,omitempty"`
}

// Result values reported in Entry.Result.
const (
	ResultOK      = "ok"      // footer says ok, or no footer but no failed step
	ResultFail    = "fail"    // footer says fail, or a --- FAIL: line is present
	ResultUnknown = "unknown" // no footer and the run left no decisive marker
)

// Filter narrows List / Prune to a subset of the run logs.
type Filter struct {
	Lab     string        // exact lab name match when non-empty
	Command string        // exact command match when non-empty ("run", "test", ...)
	Since   time.Duration // only logs started within this window when > 0
	Last    int           // keep only the N newest after the above filters when > 0
}

// Dir returns (creating if needed) ~/.astrona/logs, the single directory
// every run log is written to. It is the one place this path is defined;
// internal/ui calls through here.
func Dir() (string, error) {
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

// List returns every run log matching f, newest first, with Index assigned
// 1..N in that order (so index 1 is always the most recent run).
func List(f Filter) ([]Entry, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		return nil, fmt.Errorf("scan log directory %s: %w", dir, err)
	}

	var out []Entry
	for _, p := range matches {
		e, err := parseEntry(p)
		if err != nil {
			// A single unreadable file must not sink the whole listing —
			// skip it and keep going.
			continue
		}
		if !f.matches(e) {
			continue
		}
		out = append(out, e)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })

	if f.Since > 0 {
		cutoff := time.Now().Add(-f.Since)
		kept := out[:0]
		for _, e := range out {
			if e.Started.After(cutoff) {
				kept = append(kept, e)
			}
		}
		out = kept
	}

	if f.Last > 0 && len(out) > f.Last {
		out = out[:f.Last]
	}

	for i := range out {
		out[i].Index = i + 1
	}
	return out, nil
}

func (f Filter) matches(e Entry) bool {
	if f.Lab != "" && e.Lab != f.Lab {
		return false
	}
	if f.Command != "" && e.Command != f.Command {
		return false
	}
	return true
}

// Resolve maps a user-supplied reference to a single Entry. Accepted forms:
//
//	""           the most recent run
//	"latest"     the most recent run
//	"3"          the run at index 3 in List(Filter{}) order (1 = newest)
//	"20260830T120000Z"  a run whose filename carries that UTC stamp
//	"run-demo-20260830T120000Z.log" or an absolute path to the file
//
// f pre-narrows the candidate set for "", "latest", and index refs (e.g.
// Resolve("", Filter{Command: "run"}) is the newest run log).
func Resolve(ref string, f Filter) (Entry, error) {
	entries, err := List(f)
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, fmt.Errorf("no run logs found under ~/.astrona/logs")
	}

	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "latest" {
		return entries[0], nil
	}

	if n, err := strconv.Atoi(ref); err == nil {
		if n < 1 || n > len(entries) {
			return Entry{}, fmt.Errorf("log index %d out of range (have 1..%d)", n, len(entries))
		}
		return entries[n-1], nil
	}

	// Path or filename: match against the full candidate set (unfiltered),
	// since an explicit path is unambiguous on its own.
	all, err := List(Filter{})
	if err != nil {
		return Entry{}, err
	}
	want := filepath.Base(ref)
	for _, e := range all {
		if e.Path == ref || e.File == want {
			return e, nil
		}
	}

	// Bare stamp.
	for _, e := range all {
		if strings.Contains(e.File, ref) {
			return e, nil
		}
	}

	return Entry{}, fmt.Errorf("no run log matches %q", ref)
}

// Copy streams the raw bytes of e's log file to w.
func Copy(w io.Writer, e Entry) error {
	f, err := os.Open(e.Path)
	if err != nil {
		return fmt.Errorf("open log %s: %w", e.Path, err)
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("read log %s: %w", e.Path, err)
	}
	return nil
}

// Line is one classified line of a parsed log.
type Line struct {
	Kind string `json:"kind" yaml:"kind"`
	Text string `json:"text" yaml:"text"`
}

// Line kinds.
const (
	KindHeader  = "header"
	KindSection = "section"
	KindStep    = "step"
	KindOK      = "ok"
	KindSkip    = "skip"
	KindFail    = "fail"
	KindWarn    = "warn"
	KindDone    = "done"
	KindOutput  = "output"
)

// Log is a fully parsed run log: its metadata plus every line tagged with
// what it is. This is what `astrona logs view --format json|yaml` emits.
type Log struct {
	Meta  Entry  `json:"meta" yaml:"meta"`
	Lines []Line `json:"lines" yaml:"lines"`
}

// Parse reads e's log file and classifies every line.
func Parse(e Entry) (Log, error) {
	f, err := os.Open(e.Path)
	if err != nil {
		return Log{}, fmt.Errorf("open log %s: %w", e.Path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanLine)

	out := Log{Meta: e}
	for sc.Scan() {
		out.Lines = append(out.Lines, Line{Kind: classify(sc.Text()), Text: sc.Text()})
	}
	if err := sc.Err(); err != nil {
		return Log{}, fmt.Errorf("read log %s: %w", e.Path, err)
	}
	return out, nil
}

func classify(line string) string {
	switch {
	case strings.HasPrefix(line, "# done:"):
		return KindDone
	case strings.HasPrefix(line, "# "):
		return KindHeader
	case strings.HasPrefix(line, "=== ") && strings.HasSuffix(line, " ==="):
		return KindSection
	case strings.HasPrefix(line, "--- ok: "):
		return KindOK
	case strings.HasPrefix(line, "--- skip: "):
		return KindSkip
	case strings.HasPrefix(line, "--- FAIL: "):
		return KindFail
	case strings.HasPrefix(line, "[WARN] "):
		return KindWarn
	case strings.HasPrefix(line, "--- ") && strings.HasSuffix(line, " ---"):
		return KindStep
	default:
		return KindOutput
	}
}

// Prune deletes run logs matching f, always retaining the keep newest of
// them and (when olderThan > 0) only ever deleting logs older than that.
// With dryRun set nothing is removed; the returned slice is what would be
// (or was) deleted, newest first.
func Prune(f Filter, keep int, olderThan time.Duration, dryRun bool) ([]Entry, error) {
	entries, err := List(f)
	if err != nil {
		return nil, err
	}

	if keep < 0 {
		keep = 0
	}
	if keep >= len(entries) {
		return nil, nil
	}

	candidates := entries[keep:]

	var doomed []Entry
	if olderThan > 0 {
		cutoff := time.Now().Add(-olderThan)
		for _, e := range candidates {
			if e.Started.Before(cutoff) {
				doomed = append(doomed, e)
			}
		}
	} else {
		doomed = append(doomed, candidates...)
	}

	if dryRun {
		return doomed, nil
	}

	for _, e := range doomed {
		if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("delete %s: %w", e.Path, err)
		}
	}
	return doomed, nil
}

// parseEntry builds an Entry for a single log file: filename stat plus a
// scan of the header (line 1-2) and, if present, the footer / step tallies.
func parseEntry(path string) (Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, err
	}

	e := Entry{
		Path:   path,
		File:   filepath.Base(path),
		Size:   info.Size(),
		Result: ResultUnknown,
	}

	// Filename fallback for command / lab / start time.
	fnCmd, fnLab, fnStamp := splitFilename(e.File)
	e.Command, e.Lab = fnCmd, fnLab
	if t, err := time.Parse(stampLayout, fnStamp); err == nil {
		e.Started = t.UTC()
	} else {
		e.Started = info.ModTime()
	}

	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanLine)

	sawFail := false
	lineNo := 0
	for sc.Scan() {
		line := sc.Text()
		lineNo++

		switch {
		case lineNo == 1:
			if m := headerRe.FindStringSubmatch(line); m != nil {
				e.Command = m[1]
				e.Lab = m[2]
				if t, err := time.Parse(time.RFC3339, m[3]); err == nil {
					e.Started = t.UTC()
				}
			}
		case lineNo == 2 && strings.HasPrefix(line, "# "):
			e.Argv = strings.TrimPrefix(line, "# ")
		case strings.HasPrefix(line, "--- FAIL: "):
			sawFail = true
		case strings.HasPrefix(line, "# done:"):
			if m := footerRe.FindStringSubmatch(line); m != nil {
				e.Result = m[1]
				e.StepsOK, _ = strconv.Atoi(m[2])
				e.StepsSkp, _ = strconv.Atoi(m[3])
				e.StepsErr, _ = strconv.Atoi(m[4])
				e.Duration = m[5]
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Entry{}, err
	}

	// No footer: infer from whether any step failed.
	if e.Result == ResultUnknown && lineNo > 0 {
		if sawFail {
			e.Result = ResultFail
		} else {
			e.Result = ResultOK
		}
	}

	return e, nil
}

// splitFilename pulls command / lab / stamp out of
// "<command>-<lab>-<stamp>.log". The command token never contains a dash
// and the stamp is a fixed-width dashless timestamp, so the lab is
// whatever sits between the first dash and the last — even if it contains
// its own dashes.
func splitFilename(name string) (cmd, lab, stamp string) {
	base := strings.TrimSuffix(name, ".log")
	first := strings.Index(base, "-")
	last := strings.LastIndex(base, "-")
	if first < 0 || last <= first {
		return base, "", ""
	}
	return base[:first], base[first+1 : last], base[last+1:]
}
