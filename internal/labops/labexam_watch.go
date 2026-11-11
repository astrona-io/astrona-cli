package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"astrona/internal/account"
	"astrona/internal/hypervisor"
	"astrona/internal/ui"
)

// ExamWatchdogCommand is the hidden command `astrona hands-on-exam start` leaves
// running: `astrona exam-watchdog <attempt>`.
const ExamWatchdogCommand = "exam-watchdog"

// StartExamWatchdog starts the detached watchdog for the attempt and
// records its pid, replacing one already running for it.
func StartExamWatchdog(st *ExamState) (int, error) {
	if ExamWatchdogAlive(st) {
		stopHelper(st.WatchdogPID, ExamWatchdogCommand, st.ID)
	}
	pid, err := SpawnDetached(ExamWatchdogCommand, st.ID, ".exam-watchdog.log")
	if err != nil {
		return 0, err
	}
	st.WatchdogPID = pid
	return pid, UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s != nil && s.ID == st.ID {
			s.WatchdogPID = pid
		}
		return s, nil
	})
}

// ExamWatchdogAlive reports whether the attempt's watchdog still runs.
func ExamWatchdogAlive(st *ExamState) bool {
	if st == nil || st.WatchdogPID <= 0 || !hypervisor.ProcessAlive(st.WatchdogPID) {
		return false
	}
	cmdline, err := WatchdogCommandLine(st.WatchdogPID)
	return err == nil && isHelperFor(cmdline, ExamWatchdogCommand, st.ID)
}

// StopExamWatchdog ends the attempt's watchdog, unless that is us.
func StopExamWatchdog(st *ExamState) {
	if st != nil {
		stopHelper(st.WatchdogPID, ExamWatchdogCommand, st.ID)
	}
}

// ExamWatch follows an attempt for as long as it is open. It acts on what
// labs says, because labs owns the clock:
//
//   - on_break: pause every cluster (a break started on the exam page);
//   - running again: unpause them (resumed, or the break limit reached);
//   - running: a desktop notice at 60, 30, 15, 5 and 1 minutes left, and
//     every minute, activity reported when a shell history file changed;
//   - the deadline passed, or the attempt was submitted from the page:
//     "Time is up — grading your tasks", grade every task (as of the
//     end), send the results, delete the clusters and the exam-<n>
//     contexts;
//   - ended by time while this machine slept (it wakes after the end and
//     never graded): grade every task now, as of the end, with source wake
//     — no work can have happened while it slept — then tear down;
//   - abandoned, failed, cancelled otherwise, or gone: delete without
//     grading.
//
// When labs can't be reached it keeps the last deadline it saw and still
// grades and tears down at it. Afterwards it keeps sending what waits in
// the exam queue for as long as anything does (each signal is given up
// after ExamSignalMaxAge).
type ExamWatch struct {
	API  *ExamAPI
	ID   string
	Load ExamLabLoader
	Tick time.Duration
	Out  io.Writer
	// WithLock runs fn holding what other astrona commands hold (the
	// shared data lock), around grading and teardown.
	WithLock func(ctx context.Context, fn func() error) error
}

// examWakeLag is how late past the deadline the watchdog may notice it
// and still call its grading "deadline": later, this machine was asleep
// (or labs unreachable) and the grading is a "wake".
const examWakeLag = 2 * time.Minute

// examFlushMaxWait is the longest wait between sends of an ended exam's
// queue.
const examFlushMaxWait = 5 * time.Minute

// Run watches until the attempt is over and everything it queued is sent,
// or ctx is done.
func (w ExamWatch) Run(ctx context.Context) error {
	var deadline time.Time
	activity := examActivity{checked: time.Now()}
	wait := w.Tick
	for {
		st, err := LoadExamState()
		if err != nil {
			return err
		}
		flushed := FlushExamQueue(ctx, w.Out)
		left := ExamQueueLen(w.ID, false)
		if st == nil || st.ID != w.ID || st.Ended {
			if left == 0 {
				fmt.Fprintf(w.Out, "exam %s is over on this machine and nothing waits to be sent; stopping\n", w.ID)
				return nil
			}
			if flushed.SignedOut {
				fmt.Fprintf(w.Out, "%d exam signal(s) wait for a sign-in (astrona login); stopping — any astrona command sends them then\n", left)
				return nil
			}
			wait = min(2*wait, examFlushMaxWait) // the end is graded: only sending is left
		} else {
			if _, err := w.step(ctx, st, &deadline, &activity); err != nil {
				fmt.Fprintf(w.Out, "%s %s\n", time.Now().Format(time.RFC3339), err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// examActivity is the watchdog's shell-activity heuristic: every minute it
// looks at the shell history files' modification times (never their
// contents) and reports activity when one changed since the last look.
type examActivity struct {
	checked time.Time
}

func (a *examActivity) check(st *ExamState, now time.Time) {
	if now.Sub(a.checked) < ExamActivityEvery {
		return
	}
	since := a.checked
	a.checked = now
	if at, ok := shellActiveSince(since); ok {
		if at.After(now) {
			at = now
		}
		_ = NoteExamActivity(st, at)
	}
}

// step looks at the attempt once; done is true once it was torn down.
// activity is nil from Once (no heuristic).
func (w ExamWatch) step(ctx context.Context, st *ExamState, deadline *time.Time, activity *examActivity) (bool, error) {
	now := time.Now()
	ex, err := w.API.Get(ctx, st.ID)
	if errors.Is(err, account.ErrExamGone) {
		return true, w.finish(ctx, st, "", now, "the attempt is gone")
	}
	if err != nil {
		if !deadline.IsZero() && !now.Before(*deadline) {
			return true, w.finish(ctx, st, timeUpSource(*deadline, now), *deadline, "the time is up (labs not reachable)")
		}
		return false, err
	}
	if d, ok := ex.Deadline(); ok {
		*deadline = d
	}
	switch ex.State {
	case "on_break":
		if !st.Paused {
			fmt.Fprintf(w.Out, "%s break started: pausing the exam clusters\n", now.Format(time.RFC3339))
			return false, PauseExam(st, true)
		}
	case "running":
		if st.Paused {
			fmt.Fprintf(w.Out, "%s break over: unpausing the exam clusters\n", now.Format(time.RFC3339))
			if err := PauseExam(st, false); err != nil {
				return false, err
			}
		}
		if !deadline.IsZero() && !now.Before(*deadline) {
			return true, w.finish(ctx, st, timeUpSource(*deadline, now), *deadline, "the time is up")
		}
		if !deadline.IsZero() {
			w.countdown(st, deadline.Sub(now))
		}
		if activity != nil {
			activity.check(st, now)
		}
	case "submitted":
		return true, w.finish(ctx, st, account.ResultSubmit, endedAt(ex, *deadline, now), "the exam was submitted")
	case "expired":
		at := endedAt(ex, *deadline, now)
		return true, w.finish(ctx, st, timeUpSource(at, now), at, "the exam ended (expired)")
	case "graded", "cancelled":
		// Labs graded or cancelled it before this machine graded the end:
		// it was asleep, or no watchdog ran. Results graded before the end
		// still count, so grade now as of the end, unless it was abandoned
		// (scored from the results already sent) or never ran.
		source := ""
		if !st.Graded {
			switch ex.EndReason {
			case "submitted":
				source = account.ResultSubmit
			case "time_up", "inactive":
				source = account.ResultWake
			}
		}
		return true, w.finish(ctx, st, source, endedAt(ex, *deadline, now), "the exam ended ("+ex.State+")")
	case "abandoned", "failed":
		return true, w.finish(ctx, st, "", now, "the exam ended ("+ex.State+")")
	}
	return false, nil
}

// timeUpSource is a time-up grading's source: deadline when this machine
// noticed the end in time, wake when it noticed it late (asleep).
func timeUpSource(end, now time.Time) string {
	if now.Sub(end) > examWakeLag {
		return account.ResultWake
	}
	return account.ResultDeadline
}

// endedAt is when the attempt ended, as labs says, never after the
// deadline; else the deadline; else now.
func endedAt(ex *account.LabExam, deadline, now time.Time) time.Time {
	at := now
	if e, ok := ex.Ended(); ok {
		at = e
	}
	if !deadline.IsZero() && deadline.Before(at) {
		at = deadline
	}
	return at
}

// countdown shows the countdown notice due with left on the clock, once.
// Time added back (an extra-time grant, a break) re-arms the notices above.
func (w ExamWatch) countdown(st *ExamState, left time.Duration) {
	due, prev := examNotice(left), st.Notified
	if due == prev {
		return
	}
	st.Notified = due
	if err := UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s != nil && s.ID == st.ID {
			s.Notified = due
		}
		return s, nil
	}); err != nil {
		return
	}
	if due == 0 || (prev != 0 && due > prev) {
		return // more time than the notice shown: re-armed, nothing to say
	}
	mins := int((left + time.Minute - 1) / time.Minute)
	msg := fmt.Sprintf("%d minutes left", mins)
	if mins <= 1 {
		msg = "1 minute left"
	}
	fmt.Fprintf(w.Out, "%s notice: %s\n", time.Now().Format(time.RFC3339), msg)
	Notify(examNoticeTitle(st), msg)
}

func examNoticeTitle(st *ExamState) string {
	if st.Title != "" {
		return SanitizeLine(st.Title)
	}
	return "Astrona hands-on exam"
}

// finish grades (with source; "" grades nothing) and tears the attempt
// down.
func (w ExamWatch) finish(ctx context.Context, st *ExamState, source string, at time.Time, why string) error {
	fmt.Fprintf(w.Out, "%s %s\n", time.Now().Format(time.RFC3339), why)
	grade := source != "" && st.Ready && !st.Graded
	if grade && source != account.ResultSubmit {
		Notify(examNoticeTitle(st), "Time is up — grading your tasks")
	}
	return w.WithLock(ctx, func() error {
		rep, err := ui.New(ExamWatchdogCommand, st.ID, ui.Options{Screen: w.Out, Plain: true})
		if err != nil {
			return err
		}
		defer rep.Close()
		if grade {
			sum := GradeAndSendExam(ctx, w.API, st, w.Load, at, source, rep)
			fmt.Fprintf(w.Out, "graded %d task(s) as of %s (%s), %d waiting to be sent, %d failed\n", sum.Graded, at.Format(time.RFC3339), source, sum.Queued, len(sum.Failed))
			for _, f := range sum.Failed {
				fmt.Fprintln(w.Out, "  "+f)
			}
		}
		return TeardownExam(st, rep)
	})
}

// Once looks at the attempt a single time and acts as Run would; done is
// true once it was torn down. The exam commands use it for an attempt that
// ended while no watchdog ran (this machine was asleep or off): the end is
// graded now, as of the end, before teardown.
func (w ExamWatch) Once(ctx context.Context, st *ExamState) (bool, error) {
	var deadline time.Time
	return w.step(ctx, st, &deadline, nil)
}
