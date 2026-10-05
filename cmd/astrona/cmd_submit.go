package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"astrona/internal/config"
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
	var noHints, history bool

	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit the lab to the Proctor for grading",
		Long: "Submit the running lab to the Proctor for grading: every validation check and script " +
			"runs, a failed one shows its hint (if the lab author wrote one), and you get a score. " +
			"Every attempt is recorded — `astrona submit --history` lists them.\n\n" +
			"Passing means every check passes, or — when the lab sets validation.passPercent — " +
			"reaching that score.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			env, err := runtime.LoadEnvironment(clusterName, cfg.Runtime)
			if err != nil {
				return fmt.Errorf("could not find a running lab environment: %w", err)
			}

			rep, err := ui.NewReporter("submit", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			rep.Section("Proctor")
			pr := proctor.NewProctor(baseDir, env)
			if noHints {
				pr.HideHints()
			}
			previous, _ := proctor.LoadAttempts(clusterName)
			results, pass, err := pr.Grade(cfg)
			if err != nil {
				return err
			}

			attempt := proctor.NewAttempt(results, pass, time.Now())
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
				fmt.Printf("\nPROCTOR: FAIL\n")
				return fmt.Errorf("submission did not pass grading")
			}

			fmt.Printf("\nPROCTOR: PASS\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&junitPath, "junit-xml", "", "Write a JUnit XML test report to this path, for CI systems to parse")
	cmd.Flags().BoolVar(&noHints, "no-hints", false, "Don't show hints for failed checks (exam conditions)")
	cmd.Flags().BoolVar(&history, "history", false, "List previous attempts for this lab instead of grading")

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
	fmt.Fprintln(tw, "#\tWHEN\tSCORE\tRESULT")
	best := attempts[0]
	for i, a := range attempts {
		result := "FAIL"
		if a.Pass {
			result = "PASS"
		}
		fmt.Fprintf(tw, "%d\t%s\t%d/%d\t%s\n", i+1, a.Time.Local().Format("2006-01-02 15:04"), a.Earned, a.Max, result)
		if a.Earned > best.Earned {
			best = a
		}
	}
	tw.Flush()
	fmt.Fprintf(w, "\nBest: %d/%d · %d attempt(s)\n", best.Earned, best.Max, len(attempts))
}
