package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/proctor"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// homeLab is what plain `astrona` shows: the lab commands would act on.
type homeLab struct {
	name, cluster, source string
	cfg                   *config.LabConfig // nil for a remembered --git lab
}

// homeLabFor is the lab commands would use without naming one: -c, the
// current directory, or `astrona use` — read from local files only (a
// remembered --git lab isn't cloned just to show its name).
func homeLabFor(cmd *cobra.Command, flags *rootFlags) (homeLab, bool) {
	source := "this directory"
	switch {
	case cmd.Flags().Changed("config") || cmd.Flags().Changed("git"):
		source = "-c/--git"
	case flags.fromCurrent:
		source = "astrona use"
		if c, err := loadCurrentLab(); err == nil && c != nil && c.Git != "" {
			return homeLab{name: c.Name, cluster: config.NormalizeClusterName(c.Name), source: source}, c.Name != ""
		}
	}
	if flags.gitURL != "" {
		return homeLab{}, false
	}
	path, err := config.ResolveConfigPath(flags.configPath, flags.fileName, "", "", false)
	if err != nil {
		return homeLab{}, false
	}
	cfg, cleanup, err := config.LoadLabConfig(path)
	if err != nil {
		return homeLab{}, false
	}
	cleanup()
	if source == "this directory" {
		source = "this directory (" + filepath.Base(filepath.Dir(mustAbs(path))) + ")"
	}
	return homeLab{name: cfg.Metadata.Name, cluster: config.NormalizeClusterName(cfg.Metadata.Name), source: source, cfg: cfg}, true
}

// printHome is the top of plain `astrona`: which lab, its state, exam
// clock, last result, and what to do next.
func printHome(w io.Writer, lab homeLab, ok bool, now time.Time) {
	bold := func(s string) string { return ui.Paint(w, s, ui.Bold) }
	others := otherRunningLabs(lab.cluster)
	if !ok {
		fmt.Fprintf(w, "No lab picked.\n\n  Pick one:  astrona use <lab dir | git URL>   (or cd into a lab)\n")
		if len(others) > 0 {
			fmt.Fprintf(w, "  Running:   %s\n", strings.Join(others, ", "))
		}
		return
	}

	row, running := findLabRow(lab.cluster)
	state := "not running"
	if running {
		state = row.status + " (" + row.runtime + ")"
	}
	fmt.Fprintf(w, "%s %s  %s\n", bold("Lab"), bold(lab.name), ui.Paint(w, "(from "+lab.source+")", ui.Dim))
	fmt.Fprintf(w, "  State     %s\n", state)
	st := labStatus{row: row, cfg: lab.cfg, now: now}
	if running {
		st.exam, _ = exam.Load(lab.cluster)
		if st.exam != nil {
			fmt.Fprintf(w, "  Exam      %s\n", st.exam.Summary(now))
		}
	}
	st.attempts, _ = proctor.LoadAttempts(lab.cluster)
	if n := len(st.attempts); n > 0 {
		last := st.attempts[n-1]
		fmt.Fprintf(w, "  Last try  %s %d/%d · %s · attempt #%d\n", ui.PassFail(w, last.Pass, 0), last.Earned, last.Max, humanAgo(now.Sub(last.Time)), n)
	}
	next := "astrona run"
	if running {
		next = nextStep(st)
	}
	fmt.Fprintf(w, "\n  Next: %s\n", next)
	if len(others) > 0 {
		fmt.Fprintf(w, "\nAlso running: %s (astrona list)\n", strings.Join(others, ", "))
	}
}

// otherRunningLabs are running labs other than except (no linked
// clusters, no test copies).
func otherRunningLabs(except string) []string {
	owners := linkedClusterOwners()
	var out []string
	for _, r := range collectKindRows() {
		if _, linked := owners[r.name]; linked || r.name == except || strings.HasPrefix(r.name, "astro-test-") || r.status == "Stopped" {
			continue
		}
		out = append(out, strings.TrimPrefix(r.name, "astro-"))
	}
	return out
}

// homeRun is the root command's Run: plain `astrona` — the lab's status,
// then every command, exactly as `astrona --help` lists them.
func homeRun(flags *rootFlags) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		lab, ok := homeLabFor(cmd, flags)
		out := cmd.OutOrStdout()
		printHome(out, lab, ok, time.Now())
		fmt.Fprintln(out)
		_ = cmd.Help() // only fails writing to stdout, which has nowhere better to report
	}
}
