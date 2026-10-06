package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/junit"
	"astrona/internal/proctor"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// newSubmitCmd builds `astrona submit`: submit the already-running lab
// environment to the Proctor for grading. This is the "I'm done" entry
// point a trainee runs by hand — grading itself is owned entirely by
// Proctor.Grade, not by this command, so run/destroy/submit never read
// validation.checks/validation.script directly. flags is bound to the root
// command's persistent flags.
func newSubmitCmd(flags *rootFlags) *cobra.Command {
	var junitPath string
	var noHints, history, watch bool
	var watchInterval time.Duration

	cmd := &cobra.Command{
		Use:   "submit [lab]",
		Short: "Submit the lab to the Proctor for grading",
		Long: "Submit the running lab to the Proctor for grading: every validation check and script " +
			"runs, a failed one shows its hint (if the lab author wrote one), and you get a score. " +
			"Every attempt is recorded — `astrona submit --history` lists them. `--watch` re-grades " +
			"continuously while you work, without recording attempts.\n\n" +
			"Passing means every check passes, or — when the lab sets validation.passPercent — " +
			"reaching that score.",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			useLabArg(args, flags) // a lab given as the argument wins over `astrona use`
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)

			if history {
				attempts, err := proctor.LoadAttempts(clusterName)
				if err != nil {
					return err
				}
				printAttempts(os.Stdout, clusterName, attempts)
				return nil
			}

			// Grading runs validation scripts and command checks on this
			// machine.
			if err := requireTrust(flags, cfg, baseDir); err != nil {
				return err
			}

			env, err := runtime.LoadEnvironment(clusterName, cfg.Runtime)
			if err != nil {
				return fmt.Errorf("could not find a running lab environment: %w", err)
			}
			links, _ := labLinks(clusterName)
			env.WithLinks(links)
			env.AddEnv(labCAEnv(clusterName)...)

			rep, err := ui.NewReporter("submit", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			rep.Section("Proctor")
			var examState *exam.State
			if cfg.Exam.Enabled() {
				if examState, err = exam.Load(clusterName); err != nil {
					rep.Warn("%s", err)
				}
			}

			pr := proctor.NewProctor(baseDir, env)
			if noHints || cfg.Exam.HideHints {
				pr.HideHints()
			}
			if watch {
				rep.Close()
				return watchGrading(pr, cfg, clusterName, watchInterval, examState)
			}
			previous, _ := proctor.LoadAttempts(clusterName)
			results, pass, err := pr.Grade(cfg)
			if err != nil {
				return err
			}

			now := time.Now()
			if examState != nil {
				fmt.Printf("Time: %s\n", examState.Summary(now))
				if examState.Over(now) && cfg.Exam.Strict && pass {
					pass = false
					fmt.Printf("Submitted after the time limit — not counted as a pass (exam.strict).\n")
				}
			}

			attempt := proctor.NewAttempt(results, pass, now)
			if examState != nil {
				attempt.Timed = true
				attempt.ElapsedSeconds = int64(examState.Elapsed(now).Seconds())
				attempt.OverTime = examState.Over(now)
			}
			if err := proctor.RecordAttempt(clusterName, attempt); err != nil {
				rep.Warn("could not record this attempt: %s", err)
			}
			printProgress(os.Stdout, previous, attempt)

			if junitPath != "" {
				if err := junit.WriteJUnitReport(junitPath, clusterName, results); err != nil {
					rep.Warn("failed to write JUnit report: %s", err)
				}
			}

			if !pass {
				fmt.Printf("\nPROCTOR: %s\n", ui.PassFail(os.Stdout, false, 0))
				return fmt.Errorf("submission did not pass grading")
			}

			fmt.Printf("\nPROCTOR: %s\n", ui.PassFail(os.Stdout, true, 0))
			return nil
		},
	}

	cmd.Flags().StringVar(&junitPath, "junit-xml", "", "Write a JUnit XML test report to this path, for CI systems to parse")
	cmd.Flags().BoolVar(&noHints, "no-hints", false, "Don't show hints for failed checks (exam conditions)")
	cmd.Flags().BoolVar(&history, "history", false, "List previous attempts for this lab instead of grading")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Re-grade continuously while you work (Ctrl-C to stop; not recorded as attempts)")
	cmd.Flags().DurationVar(&watchInterval, "interval", 5*time.Second, "How often --watch re-grades (minimum 2s)")

	return cmd
}

// printProgress compares this attempt with the previous one: attempt
// number, score change, and which checks flipped.
func printProgress(w io.Writer, previous []proctor.Attempt, now proctor.Attempt) {
	n := len(previous) + 1
	if len(previous) == 0 {
		fmt.Fprintf(w, "\nAttempt #%d recorded (`astrona submit --history` lists attempts).\n", n)
		return
	}
	last := previous[len(previous)-1]
	delta := now.Earned - last.Earned
	change := "no change"
	switch {
	case delta > 0:
		change = fmt.Sprintf("+%d point(s)", delta)
	case delta < 0:
		change = fmt.Sprintf("%d point(s)", delta)
	}
	fmt.Fprintf(w, "\nAttempt #%d: %d/%d (%s since #%d)\n", n, now.Earned, now.Max, change, n-1)

	before := map[string]bool{}
	for _, c := range last.Checks {
		before[c.Name] = c.Pass
	}
	for _, c := range now.Checks {
		if was, ok := before[c.Name]; ok && was != c.Pass {
			if c.Pass {
				fmt.Fprintf(w, "  now passing: %s\n", c.Name)
			} else {
				fmt.Fprintf(w, "  now failing: %s\n", c.Name)
			}
		}
	}
}

func printAttempts(w io.Writer, lab string, attempts []proctor.Attempt) {
	if len(attempts) == 0 {
		fmt.Fprintf(w, "No attempts recorded for %s yet — `astrona submit` grades and records one.\n", lab)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	timed := false
	for _, a := range attempts {
		timed = timed || a.Timed
	}
	header := "#\tWHEN\tSCORE\tRESULT"
	if timed {
		header += "\tTIME USED"
	}
	fmt.Fprintln(tw, header)
	best := attempts[0]
	for i, a := range attempts {
		result := "FAIL"
		if a.Pass {
			result = "PASS"
		}
		line := fmt.Sprintf("%d\t%s\t%d/%d\t%s", i+1, a.Time.Local().Format("2006-01-02 15:04"), a.Earned, a.Max, result)
		if timed {
			used := "-"
			if a.Timed {
				used = exam.Round(time.Duration(a.ElapsedSeconds) * time.Second)
				if a.OverTime {
					used += " (over)"
				}
			}
			line += "\t" + used
		}
		fmt.Fprintln(tw, line)
		if a.Earned > best.Earned {
			best = a
		}
	}
	tw.Flush()
	fmt.Fprintf(w, "\nBest: %d/%d · %d attempt(s)\n", best.Earned, best.Max, len(attempts))
}
