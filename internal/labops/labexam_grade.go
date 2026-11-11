package labops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"astrona/internal/account"
	"astrona/internal/lifecycle"
	"astrona/internal/proctor"
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
	env, err := examEnvironment(task.Cluster, name, examRuntime(profile))
	if err != nil {
		return Submission{}, fmt.Errorf("cluster %d is not reachable: %w", task.Cluster, err)
	}
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

// SendExamResult sends a graded task to labs with a fresh idempotency key,
// its grading time and source (account.ResultSwitch, ...). One that can't
// be sent now (offline, signed out, the site down) waits in the exam queue
// and goes out with the next exam command, the watchdog or the background
// sender; queued is then true. Only a result labs refuses for good (graded
// after the end or during a break, or an unknown task) is dropped, with the
// error.
func SendExamResult(ctx context.Context, api *ExamAPI, st *ExamState, position int, result Submission, at time.Time, source string) (queued bool, err error) {
	raw, err := json.Marshal(CapCheckTexts(result))
	if err != nil {
		return false, err
	}
	sig := ExamSignal{Attempt: st.ID, Site: api.Site, Kind: ExamSignalResult, Position: position, Key: newResultKey(), Result: raw, Source: source, At: at.UTC()}
	if ExamQueueLen(st.ID, false) == 0 {
		// Nothing waits ahead of it: send it straight away.
		sctx, cancel := context.WithTimeout(ctx, SignalTimeout)
		err = api.PostResult(sctx, st.ID, position, raw, sig.Key, sig.At, source)
		cancel()
		if err == nil || resultRefused(err) {
			return false, err
		}
	}
	if qerr := QueueExamSignal(sig); qerr != nil {
		if err == nil {
			return false, fmt.Errorf("could not keep the result to send: %w", qerr)
		}
		return false, fmt.Errorf("%w (and it could not be kept to send later: %w)", err, qerr)
	}
	FlushExamQueue(ctx, nil)
	return ExamQueueHas(st.ID, sig.Key), nil
}

// ExamQueueHas reports whether the signal with key still waits.
func ExamQueueHas(attempt, key string) bool {
	path, err := examQueuePath()
	if err != nil {
		return false
	}
	q, _ := readExamQueue(path)
	for _, s := range q {
		if s.Attempt == attempt && s.Key == key {
			return true
		}
	}
	return false
}

// ExamGradeSummary is what GradeAndSendExam did, without scores: the
// student sees those only on the result page.
type ExamGradeSummary struct {
	Graded int // tasks graded and sent
	Queued int // graded, waiting to be sent
	Failed []string
}

// GradeAndSendExam grades every task against its cluster, as of at, and
// sends each result with source. A task that can't be graded or sent is
// listed in Failed and the rest go on: every task should get a result.
// Afterwards the local record says the end was graded here (Graded).
func GradeAndSendExam(ctx context.Context, api *ExamAPI, st *ExamState, load ExamLabLoader, at time.Time, source string, rep *ui.Reporter) ExamGradeSummary {
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
		queued, err := SendExamResult(context.WithoutCancel(ctx), api, st, t.Position, result, at, source)
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
	st.Graded = true
	_ = UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s != nil && s.ID == st.ID {
			s.Graded = true
		}
		return s, nil
	})
	return sum
}

// ErrExamOnBreak: the attempt is on a break; nothing is graded until it ends.
var ErrExamOnBreak = errors.New("the exam is on a break: resume it first (astrona hands-on-exam resume)")

// ExamOnBreak reports whether ex is on a break.
func ExamOnBreak(ex *account.LabExam) bool { return ex != nil && ex.State == "on_break" }
