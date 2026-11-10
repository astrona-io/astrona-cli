package labops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"astrona/internal/account"
	"astrona/internal/lifecycle"
	"astrona/internal/proctor"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// GradeExamTask runs a task's checks against the cluster it was assigned,
// quietly and without hints: the student sees per-task results only on the
// result page. A check that fails is a result; an error means the task
// could not be graded at all (lab not loadable, cluster gone).
func GradeExamTask(ctx context.Context, st *ExamState, task ExamTaskRef, load ExamLabLoader, at time.Time) (Submission, error) {
	profile, err := st.KindProfile()
	if err != nil {
		return Submission{}, err
	}
	name := st.ClusterName(task.Cluster)
	if name == "" {
		return Submission{}, fmt.Errorf("task %d is on cluster %d, which this attempt doesn't have", task.Position, task.Cluster)
	}
	cfg, baseDir, cleanup, err := load(task.Lab)
	if err != nil {
		return Submission{}, err
	}
	defer cleanup()
	if err := lifecycle.Validate(cfg); err != nil {
		return Submission{}, err
	}
	env, err := runtime.LoadEnvironment(name, examRuntime(profile))
	if err != nil {
		return Submission{}, fmt.Errorf("cluster %d is not reachable: %w", task.Cluster, err)
	}
	env.AddEnv(lifecycle.CAEnv(name)...)
	pr := proctor.NewProctor(baseDir, env)
	pr.Quiet()
	pr.HideHints()
	pr.StopOn(ctx)
	results, pass, err := pr.Evaluate(cfg)
	if ctx.Err() != nil {
		return Submission{}, fmt.Errorf("%w: grading stopped", ErrInterrupted)
	}
	if err != nil {
		return Submission{}, err
	}
	// Labs matches result.lab against the catalog's config name, which is
	// the lab's metadata.name as written (no cluster prefix).
	return NewSubmission(cfg, cfg.Metadata.Name, results, pass, false, 1, true, at), nil
}

// SendExamResult sends a graded task to labs with a fresh idempotency key
// and its grading time. One that can't be sent now (offline, signed out,
// the site down) is kept in the local state and sent again by
// FlushExamResults; queued is then true. One labs refuses for good
// (past the deadline and its grace, on a break) is dropped with the error.
func SendExamResult(ctx context.Context, api *ExamAPI, st *ExamState, position int, result Submission, at time.Time) (queued bool, err error) {
	raw, err := json.Marshal(CapCheckTexts(result))
	if err != nil {
		return false, err
	}
	p := ExamPendingResult{Position: position, Key: newResultKey(), Result: raw, At: at.UTC()}
	err = api.PostResult(ctx, st.ID, position, raw, p.Key, p.At)
	if err == nil || resultRefused(err) {
		return false, err
	}
	if qerr := UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s == nil || s.ID != st.ID {
			return s, errors.New("the exam's local record is gone")
		}
		// A newer grade of the same task replaces the one waiting: the
		// latest result wins on the server too.
		kept := s.Pending[:0]
		for _, old := range s.Pending {
			if old.Position != position {
				kept = append(kept, old)
			}
		}
		s.Pending = append(kept, p)
		return s, nil
	}); qerr != nil {
		return false, fmt.Errorf("%w (and it could not be kept to send later: %w)", err, qerr)
	}
	return true, nil
}

// FlushExamResults sends the results waiting in the local state, oldest
// first, and reports how many are still waiting. Refused ones are dropped.
func FlushExamResults(ctx context.Context, api *ExamAPI, warn io.Writer) int {
	st, err := LoadExamState()
	if err != nil || st == nil || len(st.Pending) == 0 {
		return 0
	}
	sent := map[string]bool{}
	for _, p := range st.Pending {
		err := api.PostResult(ctx, st.ID, p.Position, p.Result, p.Key, p.At)
		switch {
		case err == nil:
			sent[p.Key] = true
		case resultRefused(err):
			sent[p.Key] = true
			if warn != nil {
				fmt.Fprintf(warn, "Task %d's result from %s was not accepted: %s\n", p.Position, p.At.Local().Format(time.Kitchen), err)
			}
		}
	}
	left := 0
	_ = UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s == nil || s.ID != st.ID {
			return s, nil
		}
		kept := s.Pending[:0]
		for _, p := range s.Pending {
			if !sent[p.Key] {
				kept = append(kept, p)
			}
		}
		s.Pending, left = kept, len(kept)
		if left == 0 && s.Ended {
			return nil, nil
		}
		return s, nil
	})
	return left
}

// ExamGradeSummary is what GradeAndSendExam did, without scores: the
// student sees those only on the result page.
type ExamGradeSummary struct {
	Graded int // tasks graded and sent
	Queued int // graded, waiting to be sent
	Failed []string
}

// GradeAndSendExam grades every task against its cluster, as of at, and
// sends each result. A task that can't be graded or sent is listed in
// Failed and the rest go on: every task should get a result.
func GradeAndSendExam(ctx context.Context, api *ExamAPI, st *ExamState, load ExamLabLoader, at time.Time, rep *ui.Reporter) ExamGradeSummary {
	var sum ExamGradeSummary
	rep.Section("Grading %d task(s)", len(st.Tasks))
	for _, t := range st.Tasks {
		if ctx.Err() != nil {
			sum.Failed = append(sum.Failed, fmt.Sprintf("task %d: interrupted", t.Position))
			continue
		}
		step := rep.Step("Task %d", t.Position)
		result, err := GradeExamTask(ctx, st, t, load, at)
		if err != nil {
			sum.Failed = append(sum.Failed, fmt.Sprintf("task %d: %s", t.Position, err))
			step.Fail(err)
			continue
		}
		queued, err := SendExamResult(context.WithoutCancel(ctx), api, st, t.Position, result, at)
		switch {
		case err != nil:
			sum.Failed = append(sum.Failed, fmt.Sprintf("task %d: %s", t.Position, err))
			step.Fail(err)
		case queued:
			sum.Queued++
			step.Skip("graded; sending later")
		default:
			sum.Graded++
			step.Done()
		}
	}
	return sum
}

// ErrExamOnBreak: the attempt is on a break; nothing is graded until it ends.
var ErrExamOnBreak = errors.New("the exam is on a break: resume it first (astrona exam resume)")

// ExamOnBreak reports whether ex is on a break.
func ExamOnBreak(ex *account.LabExam) bool { return ex != nil && ex.State == "on_break" }
