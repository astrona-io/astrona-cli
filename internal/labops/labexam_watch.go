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

// ExamWatchdogCommand is the hidden command `astrona exam start` leaves
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
//   - the deadline passed, or the attempt was submitted from the page:
//     grade every task (as of the deadline), send the results, delete the
//     clusters and the exam-<n> contexts;
//   - abandoned, failed, cancelled or gone: delete without grading.
//
// When labs can't be reached it keeps the last deadline it saw and still
// grades and tears down at it. Results that can't be sent stay in the
// local state; it keeps sending them for a while after the end.
type ExamWatch struct {
	API  *ExamAPI
	ID   string
	Load ExamLabLoader
	Tick time.Duration
	Out  io.Writer
	// WithLock runs fn holding what other astrona commands hold (the
	// shared data lock), around grading and teardown.
	WithLock func(ctx context.Context, fn func() error) error
	// FlushFor is how long results are retried after the end.
	FlushFor time.Duration
}

// Run watches until the attempt is over and its results are sent (or
// FlushFor passed), or ctx is done.
func (w ExamWatch) Run(ctx context.Context) error {
	var deadline time.Time
	var endedAt time.Time
	for {
		st, err := LoadExamState()
		if err != nil {
			return err
		}
		if st == nil || st.ID != w.ID {
			fmt.Fprintf(w.Out, "exam %s is no longer on this machine; stopping\n", w.ID)
			return nil
		}
		if left := FlushExamResults(ctx, w.API, w.Out); st.Ended {
			if left == 0 {
				return nil
			}
			if endedAt.IsZero() {
				endedAt = time.Now()
			}
			if time.Since(endedAt) > w.FlushFor {
				fmt.Fprintf(w.Out, "%d result(s) still unsent; the next astrona exam command sends them\n", left)
				return nil
			}
		} else if done, err := w.step(ctx, st, &deadline); err != nil {
			fmt.Fprintf(w.Out, "%s %s\n", time.Now().Format(time.RFC3339), err)
		} else if done {
			endedAt = time.Now()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.Tick):
		}
	}
}

// step looks at the attempt once; done is true once it was torn down.
func (w ExamWatch) step(ctx context.Context, st *ExamState, deadline *time.Time) (bool, error) {
	now := time.Now()
	ex, err := w.API.Get(ctx, st.ID)
	if errors.Is(err, account.ErrExamGone) {
		return true, w.finish(ctx, st, false, now, "the attempt is gone")
	}
	if err != nil {
		if !deadline.IsZero() && !now.Before(*deadline) {
			return true, w.finish(ctx, st, true, *deadline, "the time is up (labs not reachable)")
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
			return true, w.finish(ctx, st, true, *deadline, "the time is up")
		}
	case "submitted", "expired":
		at := now
		if !deadline.IsZero() && deadline.Before(at) {
			at = *deadline
		}
		return true, w.finish(ctx, st, true, at, "the exam ended ("+ex.State+")")
	case "graded", "abandoned", "failed", "cancelled":
		return true, w.finish(ctx, st, false, now, "the exam ended ("+ex.State+")")
	}
	return false, nil
}

// finish grades (when grade) and tears the attempt down.
func (w ExamWatch) finish(ctx context.Context, st *ExamState, grade bool, at time.Time, why string) error {
	fmt.Fprintf(w.Out, "%s %s\n", time.Now().Format(time.RFC3339), why)
	return w.WithLock(ctx, func() error {
		rep, err := ui.New(ExamWatchdogCommand, st.ID, ui.Options{Screen: w.Out, Plain: true})
		if err != nil {
			return err
		}
		defer rep.Close()
		if grade && st.Ready {
			sum := GradeAndSendExam(ctx, w.API, st, w.Load, at, rep)
			fmt.Fprintf(w.Out, "graded %d task(s), %d waiting to be sent, %d failed\n", sum.Graded, sum.Queued, len(sum.Failed))
			for _, f := range sum.Failed {
				fmt.Fprintln(w.Out, "  "+f)
			}
		}
		return TeardownExam(st, rep)
	})
}

// Once looks at the attempt a single time and acts as Run would; done is
// true once it was torn down. `astrona exam resume` uses it for an attempt
// that ended while no watchdog ran.
func (w ExamWatch) Once(ctx context.Context, st *ExamState) (bool, error) {
	var deadline time.Time
	return w.step(ctx, st, &deadline)
}
