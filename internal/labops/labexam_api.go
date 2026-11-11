package labops

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"astrona/internal/account"
)

// ExamAPI is the signed-in account's access to labs' lab-exam routes
// (through astrona-web's /api/cli/lab-exams).
type ExamAPI struct {
	Client *account.Client
	Store  account.Store
	Site   string
}

// NewExamAPI uses the saved sign-in; site "" means the configured site.
func NewExamAPI(site string) (*ExamAPI, error) {
	client, store, configured, err := AccountDeps()
	if err != nil {
		return nil, err
	}
	if site == "" {
		site = configured
	}
	return &ExamAPI{Client: client, Store: store, Site: site}, nil
}

func (a *ExamAPI) creds(ctx context.Context) (*account.Credentials, error) {
	cr, err := a.Client.Active(ctx, a.Store, a.Site)
	if err != nil {
		return nil, err
	}
	if cr == nil {
		return nil, account.ErrSignedOut
	}
	return cr, nil
}

// Available is GET /lab-exams/available?exam=.
func (a *ExamAPI) Available(ctx context.Context, exam string) (*account.LabExamAvailability, error) {
	cr, err := a.creds(ctx)
	if err != nil {
		return nil, err
	}
	return a.Client.LabExamAvailable(ctx, a.Store, cr, exam)
}

// Start draws a new attempt.
func (a *ExamAPI) Start(ctx context.Context, exam string) (*account.LabExam, error) {
	cr, err := a.creds(ctx)
	if err != nil {
		return nil, err
	}
	return a.Client.StartLabExam(ctx, a.Store, cr, exam)
}

// Get reads the attempt: state, clock, breaks and tasks.
func (a *ExamAPI) Get(ctx context.Context, id string) (*account.LabExam, error) {
	cr, err := a.creds(ctx)
	if err != nil {
		return nil, err
	}
	return a.Client.GetLabExam(ctx, a.Store, cr, id)
}

// Progress reports preparation: preparing, ready or failed. task, when
// non-zero, is the task whose setup the step says was applied.
func (a *ExamAPI) Progress(ctx context.Context, id, phase, message string, task int) error {
	cr, err := a.creds(ctx)
	if err != nil {
		return err
	}
	return a.Client.LabExamProgress(ctx, a.Store, cr, id, phase, message, task)
}

// OpenTask records that the student moved to task position at at.
func (a *ExamAPI) OpenTask(ctx context.Context, id string, position int, at time.Time) error {
	cr, err := a.creds(ctx)
	if err != nil {
		return err
	}
	return a.Client.OpenLabExamTask(ctx, a.Store, cr, id, position, at)
}

// Activity tells labs the student was working at at.
func (a *ExamAPI) Activity(ctx context.Context, id string, at time.Time, key string) error {
	cr, err := a.creds(ctx)
	if err != nil {
		return err
	}
	return a.Client.PostLabExamActivity(ctx, a.Store, cr, id, at, key)
}

// PostResult sends one task's graded result; source is what made it
// (account.ResultSwitch, ResultSubmit, ResultDeadline, ResultCheck or
// ResultWake).
func (a *ExamAPI) PostResult(ctx context.Context, id string, position int, result json.RawMessage, key string, at time.Time, source string) error {
	cr, err := a.creds(ctx)
	if err != nil {
		return err
	}
	return a.Client.PostLabExamResult(ctx, a.Store, cr, id, position, account.ResultPost{Result: result, Key: key, At: at}, source)
}

// StartBreak starts a break at at.
func (a *ExamAPI) StartBreak(ctx context.Context, id string, at time.Time) (*account.LabExam, error) {
	cr, err := a.creds(ctx)
	if err != nil {
		return nil, err
	}
	return a.Client.StartLabExamBreak(ctx, a.Store, cr, id, at)
}

// StopBreak ends the running break at at.
func (a *ExamAPI) StopBreak(ctx context.Context, id string, at time.Time) (*account.LabExam, error) {
	cr, err := a.creds(ctx)
	if err != nil {
		return nil, err
	}
	return a.Client.StopLabExamBreak(ctx, a.Store, cr, id, at)
}

// Submit ends the attempt early.
func (a *ExamAPI) Submit(ctx context.Context, id string) (*account.LabExam, error) {
	cr, err := a.creds(ctx)
	if err != nil {
		return nil, err
	}
	return a.Client.SubmitLabExam(ctx, a.Store, cr, id)
}

// Abandon ends the attempt without grading.
func (a *ExamAPI) Abandon(ctx context.Context, id string) (*account.LabExam, error) {
	cr, err := a.creds(ctx)
	if err != nil {
		return nil, err
	}
	return a.Client.AbandonLabExam(ctx, a.Store, cr, id)
}

// resultRefused reports whether labs refused a result for good: graded
// after the attempt ended (409 after_end), during a break (409 on_break),
// or for a task the attempt doesn't have. Only these drop a result; any
// other error keeps it to send again, because labs takes a result graded
// before the end at any time.
func resultRefused(err error) bool {
	return errors.Is(err, account.ErrExamAfterEnd) || errors.Is(err, account.ErrExamBreakResult) ||
		errors.Is(err, account.ErrExamUnknownTask)
}

// examAnswered reports whether labs answered a request with a refusal (as
// opposed to not being reached, or the sign-in being gone): sending the
// same task open, break or activity again can't help.
func examAnswered(err error) bool {
	if err == nil || errors.Is(err, account.ErrSignedOut) {
		return false
	}
	for _, refused := range []error{account.ErrExamGone, account.ErrExamState, account.ErrExamUnavailable,
		account.ErrExamPastDeadline, account.ErrBreakLimit, account.ErrExamOpen, account.ErrExamAfterEnd,
		account.ErrExamBreakResult, account.ErrExamUnknownTask} {
		if errors.Is(err, refused) {
			return true
		}
	}
	return account.Permanent(err)
}

// ExamAnswered is examAnswered for cmd: labs refused, rather than wasn't
// reached.
func ExamAnswered(err error) bool { return examAnswered(err) }
