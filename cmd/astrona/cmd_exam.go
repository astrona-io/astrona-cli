package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/localdata"
	"astrona/internal/preflight"
	"astrona/internal/settings"
	"astrona/internal/ui"
	"astrona/internal/version"
)

// examParallelDefault is how many exam clusters are built at once unless
// --parallel says otherwise: the CLI's limit, since every cluster is needed
// before the clock can start.
const examParallelDefault = settings.MaxParallel

func newExamCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exam",
		Short: "Take a full lab exam: several tasks on prepared clusters, against the clock",
		Long: `Take a full lab exam, such as the CKS practice exam, on this computer.

astrona exam start <exam> checks that the exam's clusters fit your container
engine, draws the tasks, builds the clusters in parallel and applies every
task's setup. The clock does not run while it prepares: it starts when you
press Begin exam on the exam page. Each task names the kubectl context to use
(exam-1, exam-2, ...); switching to it is up to you, as in the real exam.

Work on the tasks in any order. Take up to the exam's number of breaks
(astrona exam break); a break pauses every exam cluster. astrona exam submit,
or the deadline, grades every task on its cluster, sends the results and
deletes the clusters. Results show on the exam page.`,
		Example: `  astrona exam start cks
  astrona exam status
  astrona exam task 3
  astrona exam next
  astrona exam break
  astrona exam resume
  astrona exam submit`,
	}
	cmd.AddCommand(
		newExamStartCmd(flags),
		newExamStatusCmd(),
		newExamTaskCmd(flags),
		newExamStepCmd(flags, "next", 1),
		newExamStepCmd(flags, "prev", -1),
		newExamBreakCmd(),
		newExamResumeCmd(flags),
		newExamSubmitCmd(flags),
		newExamAbandonCmd(),
	)
	return cmd
}

func newExamStartCmd(flags *rootFlags) *cobra.Command {
	var noBrowser bool
	parallel := examParallelDefault
	cmd := &cobra.Command{
		Use:   "start <exam>",
		Short: "Prepare a full lab exam's clusters and open the exam page",
		Long: `Prepare a full lab exam on this computer.

The exam's clusters must fit within 75% of your container engine's memory,
counting other kind clusters that are running; if they don't, nothing is
started and astrona says how much memory the exam needs. Otherwise the tasks
are drawn, the clusters are built (up to --parallel at once) and each task's
setup is applied to its cluster. If anything fails, every exam cluster is
deleted and the attempt is not counted.

The clock starts when you press Begin exam on the exam page, not before. A
watchdog keeps running in the background: it pauses and unpauses the clusters
for breaks, and at the deadline grades every task and deletes the clusters.`,
		Example: `  astrona exam start cks
  astrona exam start cks --parallel 2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := settings.ValidateParallel(parallel); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return examStart(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), flags, strings.ToLower(strings.TrimSpace(args[0])), parallel, !noBrowser && !flags.headless)
		},
	}
	cmd.Flags().IntVar(&parallel, "parallel", examParallelDefault, fmt.Sprintf("Build up to N exam clusters at once, 1-%d", settings.MaxParallel))
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the exam page's address instead of opening it")
	return cmd
}

func examStart(ctx context.Context, out, warn io.Writer, flags *rootFlags, exam string, parallel int, browser bool) error {
	api, err := labops.NewExamAPI("")
	if err != nil {
		return err
	}
	if st, err := labops.LoadExamState(); err != nil {
		return err
	} else if st != nil {
		if !st.Ended {
			return fmt.Errorf("%w on this computer (%s): astrona exam status shows it, astrona exam resume reattaches to it, astrona exam abandon ends it", account.ErrExamOpen, examTitle(st))
		}
		labops.FlushExamResults(ctx, api, warn)
	}

	avail, err := api.Available(ctx, exam)
	if err != nil {
		return examAPIError(err)
	}
	if avail.OpenAttempt != nil && avail.OpenAttempt.Open() {
		return fmt.Errorf("%w (%s, %s): continue it with astrona exam resume %s, or end it with astrona exam abandon", account.ErrExamOpen, avail.OpenAttempt.Title, avail.OpenAttempt.State, exam)
	}
	if !avail.Available {
		reason := avail.UnavailableReason
		if reason == "" {
			reason = "it has too few labs ready"
		}
		return fmt.Errorf("%w: %s", account.ErrExamUnavailable, reason)
	}
	profile, err := labops.ParseClusterProfile(avail.ClusterProfile)
	if err != nil {
		return err
	}
	mem, err := preflight.CheckExamMemory(profile, avail.ClusterCount, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d tasks or so, %d minutes, %d breaks\n", avail.Title, avail.TasksMax, avail.TimeLimitMinutes, avail.MaxBreaks)
	fmt.Fprintf(out, "Memory: %s\n", mem.Summary())
	if err := mem.Err(); err != nil {
		return err
	}
	if err := labops.CheckExamContexts(avail.ClusterCount); err != nil {
		return err
	}

	ex, err := api.Start(ctx, exam)
	if err != nil {
		return examAPIError(err)
	}
	if len(ex.ClusterProfile) == 0 {
		ex.ClusterProfile = avail.ClusterProfile
	}
	if ex.ClusterCount == 0 {
		ex.ClusterCount = avail.ClusterCount
	}
	st := labops.NewExamState(api.Site, ex.ClusterProfile, ex.ID, ex.Exam, ex.Title, ex.URL, ex.ClusterCount, examTaskRefs(ex), time.Now())
	if st.Exam == "" {
		st.Exam = exam
	}
	if err := labops.SaveExamState(st); err != nil {
		return err
	}
	fmt.Fprintf(out, "Drew %d tasks on %d clusters. Preparing; the clock starts only when you press Begin exam.\n", len(st.Tasks), len(st.Clusters))
	fmt.Fprintf(out, "Exam page: %s\n", examPageURL(st))
	return prepareAttempt(ctx, out, warn, api, st, flags, parallel, nil, browser)
}

// prepareAttempt builds the attempt's clusters (all, or only), reports
// preparing / ready / failed while the attempt is being prepared, and
// starts the watchdog. A failed preparation deletes every exam cluster and
// fails the attempt, which then doesn't count.
func prepareAttempt(ctx context.Context, out, warn io.Writer, api *labops.ExamAPI, st *labops.ExamState, flags *rootFlags, parallel int, only []int, browser bool) error {
	profile, err := st.KindProfile()
	if err != nil {
		return err
	}
	rep, err := ui.New("exam", st.ID, ui.Options{Verbose: flags.verbose, Raw: os.Stderr})
	if err != nil {
		return err
	}
	defer rep.Close()
	preparing := !st.Ready
	req := labops.ExamPrepareRequest{State: st, Profile: profile, Load: examLabLoader(flags), Only: only, Parallel: parallel, Rep: rep}
	if preparing {
		req.Progress = func(step string) { _ = api.Progress(context.Background(), st.ID, "preparing", step) }
	}
	err = labops.PrepareExam(ctx, req)
	if err == nil {
		err = labops.AddExamContexts(st)
	}
	if err != nil {
		bg := context.WithoutCancel(ctx)
		if !preparing {
			// The clock runs: a cluster that can't be rebuilt is the
			// student's to retry, the attempt stays as it is.
			rep.Close()
			return fmt.Errorf("could not rebuild the exam clusters (try astrona exam resume again): %w", err)
		}
		reason := err.Error()
		if errors.Is(err, context.Canceled) {
			reason = "preparation was interrupted"
		}
		_ = api.Progress(bg, st.ID, "failed", reason)
		if terr := labops.TeardownExam(st, rep); terr != nil {
			rep.Warn("some exam clusters could not be deleted: %s", terr)
		}
		_ = labops.RemoveExamState()
		rep.Close()
		return fmt.Errorf("the exam could not be prepared, so the attempt doesn't count; start again for a new draw: %w", err)
	}
	if err := labops.UpdateExamState(func(s *labops.ExamState) (*labops.ExamState, error) {
		if s != nil && s.ID == st.ID {
			s.Ready = true
		}
		return s, nil
	}); err != nil {
		return err
	}
	st.Ready = true
	if preparing {
		if err := api.Progress(context.WithoutCancel(ctx), st.ID, "ready", fmt.Sprintf("All %d clusters are ready", len(st.Clusters))); err != nil {
			rep.Warn("could not tell Astrona the exam is ready: %s (astrona exam resume tries again)", err)
		}
	}
	if _, err := labops.StartExamWatchdog(st); err != nil {
		rep.Warn("could not start the exam watchdog: %s — run astrona exam resume", err)
	}
	rep.Close()

	fmt.Fprintf(out, "\nExam ready: %d clusters, contexts ", len(st.Clusters))
	for i, c := range st.Clusters {
		if i > 0 {
			fmt.Fprint(out, ", ")
		}
		fmt.Fprint(out, labops.ExamContext(c.N))
	}
	fmt.Fprintln(out, ".")
	page := examPageURL(st)
	if preparing {
		fmt.Fprintf(out, "Press Begin exam on the exam page to start the clock: %s\n", page)
		if browser && browserOpener(page) {
			fmt.Fprintln(out, "Opened the exam page in your browser.")
		}
	}
	fmt.Fprintln(out, "Then: astrona exam task 1 shows the first task; astrona exam status shows them all.")
	return nil
}

func newExamStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the exam's state, time left, breaks and tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, api, err := loadExam()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			left := labops.FlushExamResults(cmd.Context(), api, cmd.ErrOrStderr())
			if st.Ended {
				fmt.Fprintf(out, "%s has ended; its clusters are deleted. %d result(s) still to send (sent by the next astrona exam command).\n", examTitle(st), left)
				return nil
			}
			ex, err := api.Get(cmd.Context(), st.ID)
			if err != nil {
				return examAPIError(err)
			}
			printExamStatus(out, st, ex, left)
			return nil
		},
	}
}

func printExamStatus(out io.Writer, st *labops.ExamState, ex *account.LabExam, pending int) {
	fmt.Fprintf(out, "%s: %s\n", examTitle(st), examStateText(ex.State))
	switch ex.State {
	case "preparing":
		if step, ok := ex.LastStep(); ok {
			fmt.Fprintf(out, "Preparing: %s\n", labops.SanitizeLine(step.Message))
		}
	case "ready":
		fmt.Fprintf(out, "Press Begin exam on the exam page to start the clock: %s\n", examPageURL(st))
	}
	if ex.State == "running" || ex.State == "on_break" {
		left := time.Duration(ex.RemainingSeconds) * time.Second
		line := "Time left: " + left.Round(time.Second).String()
		if d, ok := ex.Deadline(); ok {
			line += " (until " + d.Local().Format("15:04") + ")"
		}
		fmt.Fprintln(out, line)
	}
	if ex.Breaks.Max > 0 {
		fmt.Fprintf(out, "Breaks: %d of %d used, each up to %d minutes\n", ex.Breaks.Used, ex.Breaks.Max, ex.Breaks.LimitSeconds/60)
	}
	if ex.State == "on_break" && ex.Breaks.Current != nil {
		if t, err := time.Parse(time.RFC3339Nano, ex.Breaks.Current.EndsAt); err == nil {
			fmt.Fprintf(out, "On a break until %s; astrona exam resume ends it now\n", t.Local().Format("15:04"))
		}
	}
	paused := ""
	if st.Paused {
		paused = " (paused)"
	}
	fmt.Fprintf(out, "Clusters: %d%s\n\nTasks:\n", len(st.Clusters), paused)
	for _, t := range ex.Tasks {
		marker := "  "
		if t.Position == st.Current {
			marker = "> "
		}
		opened := ""
		if t.Opened {
			opened = "  (opened)"
		}
		fmt.Fprintf(out, "%s%2d  %5.2f%%  %-7s  %s%s\n", marker, t.Position, t.Weight, examTaskContext(t), labops.SanitizeLine(t.Title), opened)
	}
	if pending > 0 {
		fmt.Fprintf(out, "\n%d graded result(s) waiting to be sent.\n", pending)
	}
	if !labops.ExamWatchdogAlive(st) {
		fmt.Fprintln(out, "\nThe exam watchdog is not running: astrona exam resume restarts it.")
	}
}

func newExamTaskCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "task <n>",
		Short: "Show task n and the kubectl context it uses",
		Long: `Show a task's question and the kubectl context to use for it.

astrona does not switch the context for you: run the kubectl config
use-context command the task names. Moving to another task quietly grades the
one you leave, so a recent score is kept if your computer fails; you see no
result until the exam is graded.`,
		Example: "  astrona exam task 7",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[0])
			if err != nil || n < 1 {
				return fmt.Errorf("task must be a number from 1, got %q", args[0])
			}
			return examShowTask(cmd, flags, func(int, int) (int, error) { return n, nil })
		},
	}
}

func newExamStepCmd(flags *rootFlags, name string, delta int) *cobra.Command {
	short := "Show the next task"
	if delta < 0 {
		short = "Show the previous task"
	}
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return examShowTask(cmd, flags, func(current, count int) (int, error) {
				n := current + delta
				if current == 0 {
					n = 1
				}
				switch {
				case n > count:
					return 0, fmt.Errorf("task %d is the last one", count)
				case n < 1:
					return 0, errors.New("task 1 is the first one")
				}
				return n, nil
			})
		},
	}
}

// examShowTask shows the task pick chooses, records the switch, and
// quietly grades the task left.
func examShowTask(cmd *cobra.Command, flags *rootFlags, pick func(current, count int) (int, error)) error {
	ctx := cmd.Context()
	st, api, err := loadExam()
	if err != nil {
		return err
	}
	if st.Ended {
		return fmt.Errorf("%s has ended", examTitle(st))
	}
	labops.FlushExamResults(ctx, api, cmd.ErrOrStderr())
	ex, err := api.Get(ctx, st.ID)
	if err != nil {
		return examAPIError(err)
	}
	switch ex.State {
	case "running":
	case "on_break":
		return labops.ErrExamOnBreak
	case "preparing", "ready":
		return fmt.Errorf("the exam hasn't started: press Begin exam on the exam page (%s)", examPageURL(st))
	default:
		return fmt.Errorf("%s has ended (%s)", examTitle(st), ex.State)
	}
	n, err := pick(st.Current, len(ex.Tasks))
	if err != nil {
		return err
	}
	task, ok := ex.Task(n)
	if !ok {
		return fmt.Errorf("there is no task %d: this exam has %d", n, len(ex.Tasks))
	}
	out := cmd.OutOrStdout()
	printExamTask(out, task)

	left := st.Current
	if err := labops.UpdateExamState(func(s *labops.ExamState) (*labops.ExamState, error) {
		if s != nil && s.ID == st.ID {
			s.Current = n
		}
		return s, nil
	}); err != nil {
		return err
	}
	if err := api.OpenTask(ctx, st.ID, n); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Could not record the switch to task %d: %s\n", n, err)
	}
	if left != 0 && left != n {
		if ref, ok := st.Task(left); ok {
			at := time.Now()
			result, err := labops.GradeExamTask(ctx, st, ref, examLabLoader(flags), at)
			if err == nil {
				_, err = labops.SendExamResult(context.WithoutCancel(ctx), api, st, left, result, at)
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Could not save task %d's progress now (it is graded again at submit): %s\n", left, err)
			}
		}
	}
	return nil
}

func printExamTask(out io.Writer, t account.LabExamTask) {
	ctxName := examTaskContext(t)
	fmt.Fprintf(out, "Task %d (%s%%)   Use context: kubectl config use-context %s\n", t.Position, strconv.FormatFloat(t.Weight, 'f', -1, 64), ctxName)
	head := labops.SanitizeLine(t.Title)
	if t.DomainName != "" {
		head += "  ·  " + labops.SanitizeLine(t.DomainName)
	}
	fmt.Fprintf(out, "%s\n\n", head)
	if q := strings.TrimSpace(t.Question); q != "" {
		fmt.Fprintln(out, q)
	}
}

func newExamBreakCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "break",
		Short: "Start a break: the clock stops and every exam cluster is paused",
		Long: `Start a break. The exam's clock stops, and the node containers of every exam
cluster are paused, so kubectl against an exam context waits until the break
ends. A break ends with astrona exam resume, or by itself when its time limit
is reached. Tasks are not graded during a break.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, api, err := loadExam()
			if err != nil {
				return err
			}
			ex, err := api.StartBreak(ctx, st.ID, time.Now())
			if errors.Is(err, account.ErrBreakLimit) {
				return fmt.Errorf("%w: every break of this exam is used", account.ErrBreakLimit)
			}
			if err != nil {
				return examAPIError(err)
			}
			if err := labops.PauseExam(st, true); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "The break started, but some clusters could not be paused: %s\n", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Break %d of %d started; the clock is stopped and the exam clusters are paused.\n", ex.Breaks.Used, ex.Breaks.Max)
			if ex.Breaks.LimitSeconds > 0 {
				fmt.Fprintf(out, "It ends after %d minutes at most. astrona exam resume ends it now.\n", ex.Breaks.LimitSeconds/60)
			}
			return nil
		},
	}
}

func newExamResumeCmd(flags *rootFlags) *cobra.Command {
	parallel := examParallelDefault
	cmd := &cobra.Command{
		Use:   "resume [exam]",
		Short: "End a break, or reattach to the exam after a crash or on another computer",
		Long: `End a break early, or reattach to the exam.

On a break, it ends the break and unpauses the exam clusters. Otherwise it
checks the exam's clusters, rebuilds any that are gone (a reboot can remove
them) with their tasks' setups, and restarts the watchdog. Work done on a
rebuilt cluster is lost, and the clock keeps running while it rebuilds.

To continue an exam started on another computer, name the exam: every
cluster is prepared again here.`,
		Example: `  astrona exam resume
  astrona exam resume cks`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := settings.ValidateParallel(parallel); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			exam := ""
			if len(args) == 1 {
				exam = strings.ToLower(strings.TrimSpace(args[0]))
			}
			return examResume(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), flags, exam, parallel)
		},
	}
	cmd.Flags().IntVar(&parallel, "parallel", examParallelDefault, fmt.Sprintf("Rebuild up to N exam clusters at once, 1-%d", settings.MaxParallel))
	return cmd
}

func examResume(ctx context.Context, out, warn io.Writer, flags *rootFlags, exam string, parallel int) error {
	st, err := labops.LoadExamState()
	if err != nil {
		return err
	}
	if st != nil && st.Ended {
		api, err := labops.NewExamAPI(st.Site)
		if err != nil {
			return err
		}
		left := labops.FlushExamResults(ctx, api, warn)
		if exam == "" {
			fmt.Fprintf(out, "%s has ended. %d result(s) still to send.\n", examTitle(st), left)
			return nil
		}
		st = nil
	}
	if st == nil {
		if exam == "" {
			return errors.New("no exam on this computer. To continue one started elsewhere, name it: astrona exam resume <exam>")
		}
		return examAdopt(ctx, out, warn, flags, exam, parallel)
	}
	if exam != "" && exam != st.Exam {
		return fmt.Errorf("this computer has a %s exam open, not %s", st.Exam, exam)
	}
	api, err := labops.NewExamAPI(st.Site)
	if err != nil {
		return err
	}
	labops.FlushExamResults(ctx, api, warn)
	ex, err := api.Get(ctx, st.ID)
	if err != nil {
		return examAPIError(err)
	}
	if !ex.Open() {
		labops.StopExamWatchdog(st)
		w := examWatch(api, st.ID, flags, out)
		if _, err := w.Once(ctx, st); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s has ended (%s); its clusters are deleted.\n", examTitle(st), ex.State)
		return nil
	}
	if ex.State == "on_break" {
		if ex, err = api.StopBreak(ctx, st.ID, time.Now()); err != nil {
			return examAPIError(err)
		}
		fmt.Fprintln(out, "Break over; the clock runs again.")
	}
	if st.Paused {
		if err := labops.PauseExam(st, false); err != nil {
			fmt.Fprintf(warn, "Some exam clusters could not be unpaused: %s\n", err)
		}
	}
	var only []int
	if st.Ready {
		only = labops.MissingExamClusters(st)
		if len(only) == 0 {
			if err := labops.AddExamContexts(st); err != nil {
				return err
			}
			if _, err := labops.StartExamWatchdog(st); err != nil {
				return fmt.Errorf("could not start the exam watchdog: %w", err)
			}
			printExamStatus(out, st, ex, 0)
			return nil
		}
		fmt.Fprintf(out, "%d exam cluster(s) are gone; rebuilding them with their tasks' setups. The clock keeps running, and work on those tasks is lost.\n", len(only))
	} else {
		fmt.Fprintln(out, "The exam was not fully prepared; preparing it again.")
	}
	return prepareAttempt(ctx, out, warn, api, st, flags, parallel, only, false)
}

// examAdopt continues on this computer an attempt that is open in the
// account but not here: every cluster is prepared again.
func examAdopt(ctx context.Context, out, warn io.Writer, flags *rootFlags, exam string, parallel int) error {
	api, err := labops.NewExamAPI("")
	if err != nil {
		return err
	}
	avail, err := api.Available(ctx, exam)
	if err != nil {
		return examAPIError(err)
	}
	if avail.OpenAttempt == nil || !avail.OpenAttempt.Open() {
		return fmt.Errorf("you have no open %s exam; start one with astrona exam start %s", exam, exam)
	}
	ex, err := api.Get(ctx, avail.OpenAttempt.ID)
	if err != nil {
		return examAPIError(err)
	}
	if len(ex.ClusterProfile) == 0 {
		ex.ClusterProfile = avail.ClusterProfile
	}
	if ex.ClusterCount == 0 {
		ex.ClusterCount = avail.ClusterCount
	}
	profile, err := labops.ParseClusterProfile(ex.ClusterProfile)
	if err != nil {
		return err
	}
	mem, err := preflight.CheckExamMemory(profile, ex.ClusterCount, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Memory: %s\n", mem.Summary())
	if err := mem.Err(); err != nil {
		return err
	}
	if err := labops.CheckExamContexts(ex.ClusterCount); err != nil {
		return err
	}
	st := labops.NewExamState(api.Site, ex.ClusterProfile, ex.ID, exam, ex.Title, ex.URL, ex.ClusterCount, examTaskRefs(ex), time.Now())
	// Past preparation the clock may run: rebuild without reporting
	// progress, which labs only takes while preparing.
	st.Ready = ex.State != "preparing"
	if err := labops.SaveExamState(st); err != nil {
		return err
	}
	if ex.State == "on_break" {
		if _, err := api.StopBreak(ctx, st.ID, time.Now()); err != nil {
			fmt.Fprintf(warn, "Could not end the break: %s\n", err)
		}
	}
	var only []int
	if st.Ready {
		fmt.Fprintln(out, "Preparing every exam cluster on this computer. The clock keeps running; work done on the other computer is lost, results already sent are kept.")
		for _, c := range st.Clusters {
			only = append(only, c.N)
		}
	}
	return prepareAttempt(ctx, out, warn, api, st, flags, parallel, only, false)
}

func newExamSubmitCmd(flags *rootFlags) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Grade every task, delete the exam clusters and end the exam",
		Long: `End the exam now. Every task is graded on its cluster and the results are sent;
then the exam clusters and the exam-<n> contexts are deleted, and kubectl goes
back to the context you had before the exam. The score shows on the exam
page. Results that can't be sent now are kept and sent by the next astrona
exam command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			st, api, err := loadExam()
			if err != nil {
				return err
			}
			if st.Ended {
				return fmt.Errorf("%s has already ended", examTitle(st))
			}
			ex, err := api.Get(ctx, st.ID)
			if err != nil {
				return examAPIError(err)
			}
			switch ex.State {
			case "running":
			case "on_break":
				return labops.ErrExamOnBreak
			case "preparing", "ready":
				return errors.New("the exam hasn't started, so there is nothing to submit: astrona exam abandon ends it")
			default:
				return fmt.Errorf("%s has already ended (%s): astrona exam resume cleans up", examTitle(st), ex.State)
			}
			if !yes && !confirmYes(os.Stdin, promptOut, "Submit the exam? Every task is graded now and the exam clusters are deleted.") {
				return errors.New("not submitted")
			}
			labops.StopExamWatchdog(st)
			out := cmd.OutOrStdout()
			rep, err := ui.New("exam-submit", st.ID, ui.Options{Verbose: flags.verbose, Raw: os.Stderr})
			if err != nil {
				return err
			}
			defer rep.Close()
			sum := labops.GradeAndSendExam(ctx, api, st, examLabLoader(flags), time.Now(), rep)
			if err := labops.TeardownExam(st, rep); err != nil {
				rep.Warn("some exam clusters could not be deleted: %s", err)
			}
			if _, err := api.Submit(context.WithoutCancel(ctx), st.ID); err != nil {
				rep.Warn("could not end the exam on Astrona (%s); it ends by itself at the deadline, with these results", err)
			}
			rep.Close()
			fmt.Fprintf(out, "\nExam submitted: %d task(s) graded", sum.Graded)
			if sum.Queued > 0 {
				fmt.Fprintf(out, ", %d waiting to be sent (the next astrona exam command sends them)", sum.Queued)
			}
			fmt.Fprintln(out, ".")
			for _, f := range sum.Failed {
				fmt.Fprintf(out, "  Not graded: %s\n", f)
			}
			fmt.Fprintf(out, "Your result: %s\n", examPageURL(st))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Submit without asking")
	return cmd
}

func newExamAbandonCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "abandon",
		Short: "End the exam now, without grading the tasks again, and delete its clusters",
		Long: `End the exam now and delete the exam clusters and contexts. The tasks are not
graded again: an exam abandoned after Begin exam is scored from the results
already sent (each task is graded quietly when you move away from it). One
abandoned before Begin exam is cancelled and doesn't count.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, api, err := loadExam()
			if err != nil {
				return err
			}
			if !yes && !confirmYes(os.Stdin, promptOut, fmt.Sprintf("Abandon %s? Nothing is graded and the exam clusters are deleted.", examTitle(st))) {
				return errors.New("not abandoned")
			}
			labops.StopExamWatchdog(st)
			if !st.Ended {
				if _, err := api.Abandon(ctx, st.ID); err != nil && !errors.Is(err, account.ErrExamGone) && !errors.Is(err, account.ErrExamState) {
					return examAPIError(err)
				}
			}
			rep, err := ui.New("exam-abandon", st.ID, ui.Options{Raw: os.Stderr})
			if err != nil {
				return err
			}
			defer rep.Close()
			terr := labops.TeardownExam(st, rep)
			rep.Close()
			if terr != nil {
				// Keep the record so running abandon again finds the rest.
				return fmt.Errorf("the exam is abandoned, but some of its clusters could not be deleted; run astrona exam abandon again: %w", terr)
			}
			if err := labops.RemoveExamState(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Exam abandoned; its clusters are deleted.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Abandon without asking")
	return cmd
}

// newExamWatchdogCmd is the detached helper astrona exam start leaves
// running (labops.ExamWatch).
func newExamWatchdogCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:    labops.ExamWatchdogCommand + " <attempt>",
		Short:  "Follow a full lab exam's clock and breaks (started by astrona exam start)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			st, err := labops.LoadExamState()
			if err != nil {
				return err
			}
			if st == nil || st.ID != args[0] {
				return nil
			}
			api, err := labops.NewExamAPI(st.Site)
			if err != nil {
				return err
			}
			return examWatch(api, st.ID, flags, os.Stdout).Run(ctx)
		},
	}
}

func examWatch(api *labops.ExamAPI, id string, flags *rootFlags, out io.Writer) labops.ExamWatch {
	return labops.ExamWatch{
		API: api, ID: id, Load: examLabLoader(flags), Tick: 15 * time.Second, Out: out, FlushFor: 15 * time.Minute,
		// The watchdog holds no data lock while it waits; grading loads labs
		// from cached checkouts, so it takes the shared lock for that.
		WithLock: func(ctx context.Context, fn func() error) error {
			s, err := localdata.DefaultStore()
			if err != nil {
				return err
			}
			release, err := s.LockWait(ctx, false, watchdogLockWait, nil)
			if err != nil {
				return err
			}
			defer release()
			return fn()
		},
	}
}

// examLabLoader loads a task's catalog lab the way astrona run would, but
// never hands it to another astrona version: an exam runs in one process.
// Only official catalog labs can be exam tasks.
func examLabLoader(flags *rootFlags) labops.ExamLabLoader {
	return func(lab string) (*config.LabConfig, string, func(), error) {
		f := *flags
		f.configPath, f.gitURL, f.gitRef, f.catalogLab, f.labArg = "", "", "", "", ""
		f.fileName = "config.yaml"
		if err := labArg([]string{lab}, &f); err != nil {
			return nil, "", func() {}, err
		}
		if f.catalogLab == "" {
			return nil, "", func() {}, fmt.Errorf("%s is not an Astrona catalog lab", lab)
		}
		loaded, err := labops.LoadLab(labOpsSource(&f), labops.LoadOptions{
			Version:     Version,
			Verbose:     f.verbose,
			VersionGate: examVersionGate,
			Trust:       trustOptions(&f),
			Log:         os.Stderr,
		})
		if err != nil {
			return nil, "", func() {}, err
		}
		if err := requireTrust(&f, loaded.Config, loaded.BaseDir); err != nil {
			loaded.Cleanup()
			return nil, "", func() {}, err
		}
		return loaded.Config, loaded.BaseDir, loaded.Cleanup, nil
	}
}

// examVersionGate refuses a task lab this astrona can't run, instead of
// handing it to another version.
func examVersionGate(constraint string, _ bool, _ func() error) error {
	c, err := version.ParseConstraint(constraint)
	if err != nil {
		return fmt.Errorf("astronaVersion: %w", err)
	}
	cur, err := version.Parse(Version)
	if err != nil || c.Allows(cur) {
		return nil // a developer build has no release number to compare
	}
	return fmt.Errorf("this exam has a lab that needs astrona %s; update astrona (astrona update) and try again", c)
}

// loadExam is the local attempt and the account it belongs to.
func loadExam() (*labops.ExamState, *labops.ExamAPI, error) {
	st, err := labops.LoadExamState()
	if err != nil {
		return nil, nil, err
	}
	if st == nil {
		return nil, nil, errors.New("no exam on this computer: astrona exam start <exam> starts one, astrona exam resume <exam> continues one started elsewhere")
	}
	api, err := labops.NewExamAPI(st.Site)
	if err != nil {
		return nil, nil, err
	}
	return st, api, nil
}

func examTaskRefs(ex *account.LabExam) []labops.ExamTaskRef {
	refs := make([]labops.ExamTaskRef, 0, len(ex.Tasks))
	for _, t := range ex.Tasks {
		refs = append(refs, labops.ExamTaskRef{Position: t.Position, Cluster: t.Cluster, Lab: t.Lab, Scope: t.Scope})
	}
	return refs
}

func examTaskContext(t account.LabExamTask) string {
	if t.Context != "" {
		return t.Context
	}
	return labops.ExamContext(t.Cluster)
}

func examTitle(st *labops.ExamState) string {
	if st.Title != "" {
		return labops.SanitizeLine(st.Title)
	}
	return "the " + strings.ToUpper(st.Exam) + " exam"
}

// examPageURL is the attempt's page: the address labs gave, when it is a
// safe one, else the site's /labs/exam/<id>.
func examPageURL(st *labops.ExamState) string {
	if st.URL != "" {
		if u, err := labops.ValidateOpenURL(st.URL); err == nil {
			return u.String()
		}
	}
	return strings.TrimRight(st.Site, "/") + "/labs/exam/" + st.ID
}

func examStateText(state string) string {
	switch state {
	case "preparing":
		return "preparing the clusters"
	case "ready":
		return "ready, the clock has not started"
	case "running":
		return "running"
	case "on_break":
		return "on a break"
	case "submitted", "expired", "abandoned":
		return state + ", waiting for grading"
	case "graded":
		return "graded: see the result on the exam page"
	case "failed", "cancelled":
		return state + " (not counted)"
	}
	return state
}

// examAPIError adds what to do to an error from the lab-exam routes.
func examAPIError(err error) error {
	switch {
	case errors.Is(err, account.ErrSignedOut):
		return fmt.Errorf("%w: sign in with astrona login first", err)
	case errors.Is(err, account.ErrExamGone):
		return fmt.Errorf("%w; astrona exam abandon cleans up this computer", err)
	}
	return err
}
