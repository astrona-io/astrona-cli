package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/proctor"

	"github.com/mattn/go-isatty"
)

// minWatchInterval keeps `submit --watch` from hammering the cluster.
const minWatchInterval = 2 * time.Second

// watchGrading re-evaluates the lab every interval and redraws a live
// board until Ctrl-C — a feedback loop while the student works. Nothing
// is recorded as an attempt; a plain `astrona submit` does that.
//
// On a terminal the board is redrawn in place; otherwise (piped, CI) a
// new board is printed only when a result changes.
func watchGrading(pr *proctor.Proctor, cfg *config.LabConfig, lab string, interval time.Duration, examState *exam.State) error {
	if interval < minWatchInterval {
		interval = minWatchInterval
	}
	pr.Quiet()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tty := isatty.IsTerminal(os.Stdout.Fd())
	var last string
	for {
		results, pass, err := pr.Evaluate(cfg)
		if err != nil {
			return err
		}
		board := renderWatchBoard(results, pass, cfg.Validation.PassPercent, !pr.HintsHidden(), examState, time.Now())
		key := watchKey(results)
		switch {
		case tty:
			fmt.Print("\033[H\033[2J")
			fmt.Printf("Watching %s — every %s · Ctrl-C to stop · %s\n", lab, interval, time.Now().Format(time.TimeOnly))
			fmt.Print(board)
		case key != last:
			fmt.Printf("--- %s\n%s", time.Now().Format(time.TimeOnly), board)
		}
		last = key

		select {
		case <-ctx.Done():
			fmt.Println("\nStopped watching. `astrona submit` records an attempt.")
			return nil
		case <-time.After(interval):
		}
	}
}

// watchKey identifies a set of results by pass/fail per check, so a
// non-terminal watcher prints only on changes.
func watchKey(results []proctor.CheckResult) string {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%s=%t;", r.Name, r.Pass)
	}
	return b.String()
}

func renderWatchBoard(results []proctor.CheckResult, pass bool, passPercent int, hints bool, examState *exam.State, now time.Time) string {
	var b strings.Builder
	w := io.Writer(&b)
	fmt.Fprintln(w)
	for _, r := range results {
		mark := "✗"
		if r.Pass {
			mark = "✓"
		}
		fmt.Fprintf(w, "  %s %s\n", mark, r.Name)
		if !r.Pass && r.Hint != "" && hints {
			fmt.Fprintf(w, "      hint: %s\n", r.Hint)
		}
	}
	s := proctor.ScoreOf(results)
	line := fmt.Sprintf("\nScore: %d/%d (%.0f%%)", s.Earned, s.Max, s.Percent())
	if passPercent > 0 {
		line += fmt.Sprintf(" — pass mark %d%%", passPercent)
	}
	if examState != nil {
		line += " · Time: " + examState.Summary(now)
	}
	fmt.Fprintln(w, line)
	if pass {
		fmt.Fprintf(w, "Passing — run `astrona submit` to record this attempt.\n")
	} else {
		fmt.Fprintf(w, "Not passing yet (not recorded — `astrona submit` records an attempt).\n")
	}
	return b.String()
}
