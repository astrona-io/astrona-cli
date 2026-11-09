package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/junit"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/proctor"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// newSubmitCmd builds `astrona submit`: submit the already-running lab
// environment to the Proctor for grading. This is the "I'm done" entry
// point a trainee runs by hand — grading itself is owned entirely by the
// Proctor (labops.Submit calls Proctor.Evaluate), not by this command, so run/destroy/submit never read
// validation.checks/validation.script directly. flags is bound to the root
// command's persistent flags.
func newSubmitCmd(flags *rootFlags) *cobra.Command {
	var junitPath, output string
	var noHints, history, watch, keep bool
	var watchInterval time.Duration

	cmd := &cobra.Command{
		Use:   "submit [lab]",
		Short: "Submit the lab to the Proctor for grading",
		Long: "Submit the running lab to the Proctor for grading: every validation check and script " +
			"runs, a failed one shows its hint (if the lab author wrote one), and you get a score. " +
			"Every attempt is recorded — `astrona submit --history` lists them. `--watch` re-grades " +
			"continuously while you work, without recording attempts.\n\n" +
			"Saved runs use their local config and cached repository without checking GitHub or " +
			"downloading again. Approval of the same source revision is reused.\n\n" +
			"Passing means every check passes, or — when the lab sets validation.passPercent — " +
			"reaching that score.\n\n" +
			"With no lab named, none picked with `astrona use` and no lab config in the current " +
			"directory, the only running lab is submitted; with several running, it lists them and " +
			"how to name each.\n\n" +
			"A catalog lab started while signed in (`astrona run ATS…`) sends each result to its lab " +
			"page on Astrona as well. A submit is final: PASS and FAIL both finish the attempt and stop " +
			"its timer, and once the server confirms the result, submit automatically deletes the lab, " +
			"exactly like `astrona destroy`, including in headless and JSON modes. Use --keep to keep it; " +
			"teardown.keepCluster is also respected. A local-only run has nothing to send, so its saved result finishes it too. " +
			"An unsent result of a tracked run keeps the environment. Start a new run " +
			"for another tracked attempt.\n\n" +
			"Exit code: 0 passed, 2 graded but didn't pass, 1 something else went wrong. Sending the " +
			"result to Astrona never changes the grade. A cleanup failure exits 1 and leaves the uploaded result intact.",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			if err := checkWatchFlags(watch, output, junitPath, history, keep); err != nil {
				return err
			}
			// Status lines never go to stdout under -o json: it carries
			// exactly one JSON document.
			var status io.Writer = os.Stdout
			if output == "json" {
				status = os.Stderr
			}
			name, saved, err := savedSubmission(args, flags)
			if err != nil {
				return err
			}
			if saved == nil {
				if err := labArg(args, flags); err != nil {
					return err
				}
			}
			// No lab named, none picked with `astrona use`, none here: the
			// only running lab, if there is exactly one.
			if saved == nil && len(args) == 0 && !flags.fromCurrent && !cmd.Flags().Changed("config") && !cmd.Flags().Changed("git") &&
				!cmd.Flags().Changed("file") && !labConfigHere(flags) {
				selectedName, src, pickErr := labops.PickRunningLab(labops.RunningLabNames(), labstate.Load)
				if pickErr != nil {
					return pickErr
				}
				if src != nil {
					applySource(flags, src)
					verb := "Submitting"
					if history {
						verb = "Attempts for"
					}
					fmt.Fprintf(status, "%s %s (the only running lab).\n", verb, selectedName)
				}
				name, saved, err = savedSubmission(nil, flags)
				if err != nil {
					return err
				}
			}
			if saved != nil {
				cfg, baseDir, cleanup, err := loadSavedLab(name, saved, flags)
				if err != nil {
					return err
				}
				defer cleanup()
				return submitLoadedLab(flags, cfg, baseDir, submitOptions{runID: labops.RunID(saved), junitPath: junitPath, output: output, noHints: noHints, history: history, watch: watch, keep: keep, watchInterval: watchInterval}, status)
			}
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()
			return submitLoadedLab(flags, cfg, baseDir, submitOptions{junitPath: junitPath, output: output, noHints: noHints, history: history, watch: watch, keep: keep, watchInterval: watchInterval}, status)
		},
	}

	cmd.Flags().StringVar(&junitPath, "junit-xml", "", "Write a JUnit XML test report to this path, for CI systems to parse")
	cmd.Flags().BoolVar(&noHints, "no-hints", false, "Don't show hints for failed checks (exam conditions)")
	cmd.Flags().BoolVar(&history, "history", false, "List previous attempts for this lab instead of grading")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Re-grade continuously while you work (Ctrl-C to stop; not recorded as attempts)")
	cmd.Flags().DurationVar(&watchInterval, "interval", 5*time.Second, "How often --watch re-grades (minimum 2s)")
	cmd.Flags().BoolVar(&keep, "keep", false, "Keep the lab after the result is sent to Astrona instead of automatically deleting it")
	addOutputFlag(cmd, &output)
	return cmd
}

type submitOptions struct {
	runID                         string
	junitPath, output             string
	noHints, history, watch, keep bool
	watchInterval                 time.Duration
	completed                     func(labops.Submission, bool, bool)
	// beforeCleanup runs just before a sent result deletes the lab (the UI
	// closes its terminals); deleted runs once the lab is gone.
	beforeCleanup, deleted func()
}

// submitLoadedLab is shared by submit and the workspace. Keeping the loaded
// config avoids fetching a different revision in the middle of a UI session.
// The submission itself is labops.Submit; this reads the flags, prints the
// result (text or JSON), writes the JUnit report and decides the exit code.
func submitLoadedLab(flags *rootFlags, cfg *config.LabConfig, baseDir string, options submitOptions, status io.Writer) error {
	output := options.output

	if options.history {
		clusterName, attempts, err := labops.SubmitHistory(cfg)
		if err != nil {
			return err
		}
		if output == "json" {
			if attempts == nil {
				attempts = []proctor.Attempt{}
			}
			return printJSON(attempts)
		}
		printAttempts(os.Stdout, clusterName, attempts)
		return nil
	}

	req := labops.SubmitRequest{
		Config:  cfg,
		BaseDir: baseDir,
		Lab:     labOpsSource(flags),
		RunID:   options.runID,
		Trust:   trustOptions(flags),
		UI:      ui.Options{Verbose: flags.verbose, Plain: flags.headless},
		NoHints: options.noHints,
		Out:     os.Stdout,
		Status:  status,
		Warn:    os.Stderr,
		Keep:    options.keep,
		Destroy: destroyOptions(),
		Graded: func(res *labops.SubmitResult, rep *ui.Reporter) error {
			if options.junitPath != "" {
				if err := junit.WriteJUnitReport(options.junitPath, res.ClusterName, res.Results); err != nil {
					rep.Warn("failed to write JUnit report: %s", err)
				}
			}
			if output == "json" {
				return printJSON(res.Result)
			}
			printProgress(os.Stdout, res.Previous, res.Attempt)
			fmt.Printf("\nPROCTOR: %s\n", ui.PassFail(os.Stdout, res.Passed, 0))
			return nil
		},
		BeforeCleanup: options.beforeCleanup,
		Deleted:       options.deleted,
	}
	if output == "json" {
		req.UI.Raw = os.Stderr    // stdout is the JSON result
		req.Out = nil             // no human report: the JSON is the output
		req.ScriptOut = os.Stderr // and nothing else on stdout
	}
	if options.completed != nil {
		req.Completed = func(res *labops.SubmitResult) { options.completed(res.Result, res.Sent, res.Recorded) }
	}

	if options.watch {
		s, err := labops.PrepareSubmit(req)
		if err != nil {
			return err
		}
		s.Reporter.Close()
		return watchGrading(s.Proctor, cfg, s.ClusterName, options.watchInterval, s.Exam, flags.headless)
	}

	res, err := labops.Submit(context.Background(), req)
	if err != nil {
		return err
	}
	if !res.Passed {
		if res.Finished {
			fmt.Fprintln(status, "Submission complete: FAIL. This attempt is finished; start a new run for another try.")
		} else {
			fmt.Fprintln(status, "Submission complete: FAIL. The lab stays available; start a new run for another tracked attempt.")
		}
		return notPassed("submission did not pass grading")
	}
	return nil
}

// checkWatchFlags refuses flags that --watch would otherwise silently
// ignore: a watch never records an attempt, so nothing is written, listed
// or sent.
func checkWatchFlags(watch bool, output, junitPath string, history, keep bool) error {
	if !watch {
		return nil
	}
	switch {
	case output != "":
		return fmt.Errorf("--watch redraws a live view; -o %s grades once — drop one of them", output)
	case junitPath != "":
		return fmt.Errorf("--watch records nothing, so there is no --junit-xml report — run `astrona submit --junit-xml %s` without --watch", junitPath)
	case history:
		return fmt.Errorf("--watch and --history can't be combined: --history lists attempts, --watch re-grades")
	case keep:
		return fmt.Errorf("--keep only applies after a recorded submit; --watch never sends a result or deletes the lab")
	}
	return nil
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
