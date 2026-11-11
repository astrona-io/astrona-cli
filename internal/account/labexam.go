package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Hands-on exams (`astrona hands-on-exam`): the CLI's routes under
// {site}/api/cli/lab-exams, which astrona-web forwards to the labs service.
// Labs keeps the clock; the CLI builds the clusters, grades on them and
// reports.

var (
	// ErrExamUnavailable: the site has no hands-on exam by that name, or its
	// pool is too small for a draw (HTTP 422).
	ErrExamUnavailable = errors.New("this hands-on exam is not available")
	// ErrExamOpen: an attempt is already open for this account (HTTP 409 on
	// start).
	ErrExamOpen = errors.New("you already have a hands-on exam open")
	// ErrExamGone: the site doesn't know the attempt, or it isn't this
	// account's (HTTP 404).
	ErrExamGone = errors.New("the hands-on exam attempt is gone or isn't yours")
	// ErrExamState: the attempt is not in a state that allows this (HTTP
	// 409): on a break, not running, already ended.
	ErrExamState = errors.New("the hands-on exam can't do that now")
	// ErrBreakLimit: every break of the attempt is used (HTTP 409
	// break_limit).
	ErrBreakLimit = errors.New("no breaks left")
	// ErrExamPastDeadline: the attempt's deadline and grace period are over
	// (HTTP 410). Labs no longer answers a result with it; a result is
	// only refused for good with ErrExamAfterEnd, ErrExamBreakResult or
	// ErrExamUnknownTask.
	ErrExamPastDeadline = errors.New("the hands-on exam's time is over")
	// ErrExamAfterEnd: a result graded after the attempt ended (HTTP 409
	// after_end). It can never be accepted.
	ErrExamAfterEnd = errors.New("the result was graded after the hands-on exam ended")
	// ErrExamBreakResult: a result graded during a break (HTTP 409
	// on_break). It can never be accepted.
	ErrExamBreakResult = errors.New("the result was graded during a break")
	// ErrExamUnknownTask: the attempt has no such task (HTTP 404
	// unknown_task).
	ErrExamUnknownTask = errors.New("the hands-on exam has no such task")
)

// Sources of a hands-on exam result: what made the CLI grade the task.
const (
	ResultSwitch   = "switch"   // the student moved to another task
	ResultSubmit   = "submit"   // the student submitted the exam
	ResultDeadline = "deadline" // the clock ran out while this machine was awake
	ResultCheck    = "check"    // any other grading
	ResultWake     = "wake"     // graded after the end, when this machine woke up
)

// LabExamEvent is one preparation step the attempt recorded.
type LabExamEvent struct {
	At      string `json:"at"`
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

// LabExamBreak is the running break.
type LabExamBreak struct {
	Number    int    `json:"number"`
	StartedAt string `json:"started_at"`
	EndsAt    string `json:"ends_at"`
}

// LabExamBreaks is the attempt's break allowance and use.
type LabExamBreaks struct {
	Max          int           `json:"max"`
	Used         int           `json:"used"`
	Left         int           `json:"left"`
	LimitSeconds int           `json:"limit_seconds"`
	Seconds      int           `json:"seconds"`
	Current      *LabExamBreak `json:"current"`
}

// LabExamTask is one drawn task. Cluster is 1..X; Context is the kubectl
// context the task names (exam-<cluster>). Scope is the lab's examScope
// (namespace or cluster). Weight is percent points of the whole exam.
// Graded means a result arrived for it; its score shows once the attempt
// is graded.
type LabExamTask struct {
	Position   int     `json:"position"`
	Lab        string  `json:"lab"`
	Title      string  `json:"title"`
	Question   string  `json:"question"`
	DomainID   string  `json:"domain_id"`
	DomainName string  `json:"domain_name"`
	Weight     float64 `json:"weight"`
	Cluster    int     `json:"cluster"`
	Context    string  `json:"context"`
	Scope      string  `json:"scope"`
	Opened     bool    `json:"opened"`
	Graded     bool    `json:"graded"`
}

// LabExam is an attempt as labs describes it. DeadlineAt is set once the
// clock runs (after Begin exam on the page); RemainingSeconds counts down
// only then.
type LabExam struct {
	ID               string          `json:"id"`
	Exam             string          `json:"exam"`
	Title            string          `json:"title"`
	State            string          `json:"state"`
	EndReason        string          `json:"end_reason"`
	FailureReason    string          `json:"failure_reason"`
	URL              string          `json:"url"`
	ClusterCount     int             `json:"cluster_count"`
	ClusterProfile   json.RawMessage `json:"cluster_profile,omitempty"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
	UsedSeconds      int             `json:"used_seconds"`
	RemainingSeconds int             `json:"remaining_seconds"`
	DeadlineAt       string          `json:"deadline_at"`
	// EndedAt is when the attempt ended (submitted, abandoned, time up);
	// "" while it is open.
	EndedAt string `json:"ended_at"`
	// Counted: the ended attempt counts (attempts, stats, hands-on time);
	// nil from a site that doesn't say.
	Counted         *bool          `json:"counted"`
	Breaks          LabExamBreaks  `json:"breaks"`
	PassScore       int            `json:"pass_score"`
	CurrentPosition *int           `json:"current_position"`
	Preparation     []LabExamEvent `json:"preparation"`
	Tasks           []LabExamTask  `json:"tasks"`
}

// LastStep is the latest preparation step, if any.
func (e *LabExam) LastStep() (LabExamEvent, bool) {
	if e == nil || len(e.Preparation) == 0 {
		return LabExamEvent{}, false
	}
	return e.Preparation[len(e.Preparation)-1], true
}

// Deadline is DeadlineAt parsed; ok is false before the clock runs.
func (e *LabExam) Deadline() (time.Time, bool) {
	if e == nil || e.DeadlineAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, e.DeadlineAt)
	return t, err == nil
}

// Ended is EndedAt parsed; ok is false while the attempt is open or the
// site doesn't say.
func (e *LabExam) Ended() (time.Time, bool) {
	if e == nil || e.EndedAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, e.EndedAt)
	return t, err == nil
}

// Open reports whether the attempt still runs or waits to: preparing,
// ready, running or on_break.
func (e *LabExam) Open() bool {
	if e == nil {
		return false
	}
	switch e.State {
	case "preparing", "ready", "running", "on_break":
		return true
	}
	return false
}

// Task returns the task at position.
func (e *LabExam) Task(position int) (LabExamTask, bool) {
	for _, t := range e.Tasks {
		if t.Position == position {
			return t, true
		}
	}
	return LabExamTask{}, false
}

// LabExamAvailability is GET /lab-exams/available: the exam's definition,
// whether a draw is possible, and the account's open attempt, if any.
type LabExamAvailability struct {
	Exam              string          `json:"exam"`
	Title             string          `json:"title"`
	Available         bool            `json:"available"`
	UnavailableReason string          `json:"unavailable_reason"`
	ClusterCount      int             `json:"cluster_count"`
	ClusterProfile    json.RawMessage `json:"cluster_profile"`
	TimeLimitMinutes  int             `json:"time_limit_minutes"`
	MaxBreaks         int             `json:"max_breaks"`
	BreakMinutes      int             `json:"break_minutes"`
	TasksMin          int             `json:"tasks_min"`
	TasksMax          int             `json:"tasks_max"`
	PassScore         int             `json:"pass_score"`
	// OpenAttempt is a summary (id, exam, title, state): GetLabExam has the rest.
	OpenAttempt *LabExam `json:"open_attempt"`
}

// examKey is a hands-on exam's name as the site takes it (cks).
func validExamKey(exam string) bool {
	if exam == "" || len(exam) > 32 {
		return false
	}
	for _, r := range exam {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// LabExamAvailable asks whether exam can be started, and what it builds.
func (c *Client) LabExamAvailable(ctx context.Context, s Store, cr *Credentials, exam string) (*LabExamAvailability, error) {
	exam = strings.ToLower(exam)
	if !validExamKey(exam) {
		return nil, fmt.Errorf("%q is not a hands-on exam name (for example cks)", exam)
	}
	var out LabExamAvailability
	if err := c.labExamCall(ctx, s, cr, http.MethodGet, noAttempt, "/available?exam="+url.QueryEscape(exam), nil, &out, "look up the hands-on exam"); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartLabExam draws a new attempt of exam: its tasks and the cluster each
// one is set up on. 409 wraps ErrExamOpen, 422 ErrExamUnavailable.
func (c *Client) StartLabExam(ctx context.Context, s Store, cr *Credentials, exam string) (*LabExam, error) {
	var out LabExam
	err := c.labExamCall(ctx, s, cr, http.MethodPost, noAttempt, "", map[string]string{"exam": strings.ToLower(exam)}, &out, "start the hands-on exam")
	if err != nil {
		return nil, err
	}
	if !uuidPattern.MatchString(out.ID) {
		return nil, fmt.Errorf("%s started a hands-on exam but sent no valid attempt id", cr.Site)
	}
	return &out, nil
}

// GetLabExam reads the attempt: state, clock, breaks and tasks.
func (c *Client) GetLabExam(ctx context.Context, s Store, cr *Credentials, id string) (*LabExam, error) {
	var out LabExam
	if err := c.labExamCall(ctx, s, cr, http.MethodGet, id, "", nil, &out, "read the hands-on exam"); err != nil {
		return nil, err
	}
	return &out, nil
}

// LabExamProgress reports a preparation step: phase is preparing, ready
// or failed. failed ends the attempt without counting it. Labs takes at
// most 240 characters of message. task, when non-zero on a preparing
// step, says that task's setup was applied: labs records when (setup_at).
func (c *Client) LabExamProgress(ctx context.Context, s Store, cr *Credentials, id, phase, message string, task int) error {
	if r := []rune(message); len(r) > 240 {
		message = string(r[:239]) + "…"
	}
	body := map[string]any{"phase": phase, "message": message}
	if task > 0 {
		body["task"] = task
	}
	return c.labExamCall(ctx, s, cr, http.MethodPost, id, "/progress", body, nil, "report the exam's preparation")
}

// OpenLabExamTask records that the student moved to the task at position,
// at at (zero: now, as the site sees it).
func (c *Client) OpenLabExamTask(ctx context.Context, s Store, cr *Credentials, id string, position int, at time.Time) error {
	var body any
	if !at.IsZero() {
		body = map[string]string{"at": at.UTC().Format(time.RFC3339Nano)}
	}
	return c.labExamCall(ctx, s, cr, http.MethodPost, id, "/tasks/"+strconv.Itoa(position)+"/open", body, nil, "record the task you opened")
}

// PostLabExamActivity tells labs the student was working at at (an
// `astrona hands-on-exam` command, or their shell in use). Labs uses it to tell an
// exam whose clock ran out while the student worked from one they had
// left. key makes a resend count once.
func (c *Client) PostLabExamActivity(ctx context.Context, s Store, cr *Credentials, id string, at time.Time, key string) error {
	body := map[string]string{"at": at.UTC().Format(time.RFC3339Nano), "idempotency_key": key}
	return c.labExamCall(ctx, s, cr, http.MethodPost, id, "/activity", body, nil, "record your exam activity")
}

// PostLabExamResult sends a task's graded result. The body is the lab
// session result's (result, idempotency_key, at) plus source (switch,
// submit, deadline, check or wake). Labs takes a result graded before the
// end at any time, also after grading; it refuses one for good with 409
// after_end (ErrExamAfterEnd), 409 on_break (ErrExamBreakResult) or 404
// unknown_task (ErrExamUnknownTask). Other 409s wrap ErrExamState, 422
// ErrExamUnavailable (not the task's lab).
func (c *Client) PostLabExamResult(ctx context.Context, s Store, cr *Credentials, id string, position int, p ResultPost, source string) error {
	payload := map[string]any{"result": p.Result}
	if source != "" {
		payload["source"] = source
	}
	if p.Key != "" {
		payload["idempotency_key"] = p.Key
	}
	if !p.At.IsZero() {
		payload["at"] = p.At.UTC().Format(time.RFC3339Nano)
	}
	return c.labExamCall(ctx, s, cr, http.MethodPost, id, "/tasks/"+strconv.Itoa(position)+"/results", payload, nil, "send the task's result")
}

// StartLabExamBreak starts a break (409 break_limit wraps ErrBreakLimit).
func (c *Client) StartLabExamBreak(ctx context.Context, s Store, cr *Credentials, id string, at time.Time) (*LabExam, error) {
	return c.examTransition(ctx, s, cr, id, "/breaks/start", at, "start a break")
}

// StopLabExamBreak ends the running break.
func (c *Client) StopLabExamBreak(ctx context.Context, s Store, cr *Credentials, id string, at time.Time) (*LabExam, error) {
	return c.examTransition(ctx, s, cr, id, "/breaks/stop", at, "end the break")
}

// SubmitLabExam ends the attempt as submitted.
func (c *Client) SubmitLabExam(ctx context.Context, s Store, cr *Credentials, id string) (*LabExam, error) {
	return c.examTransition(ctx, s, cr, id, "/submit", time.Time{}, "submit the hands-on exam")
}

// AbandonLabExam ends the attempt without grading.
func (c *Client) AbandonLabExam(ctx context.Context, s Store, cr *Credentials, id string) (*LabExam, error) {
	return c.examTransition(ctx, s, cr, id, "/abandon", time.Time{}, "abandon the hands-on exam")
}

func (c *Client) examTransition(ctx context.Context, s Store, cr *Credentials, id, path string, at time.Time, what string) (*LabExam, error) {
	var body any
	if !at.IsZero() {
		body = map[string]string{"at": at.UTC().Format(time.RFC3339Nano)}
	}
	var out LabExam
	if err := c.labExamCall(ctx, s, cr, http.MethodPost, id, path, body, &out, what); err != nil {
		return nil, err
	}
	return &out, nil
}

// noAttempt is labExamCall's id for a route that isn't an attempt's.
const noAttempt = "\x00"

// labExamCall sends one request to {site}/api/cli/lab-exams/{id}{rest}, or
// to {site}/api/cli/lab-exams{rest} for noAttempt. A rejected access token is renewed once and the request retried. Status
// codes map to the Err* above, with the site's message.
func (c *Client) labExamCall(ctx context.Context, s Store, cr *Credentials, method, id, rest string, body, out any, what string) error {
	path := rest
	if id != noAttempt {
		if !uuidPattern.MatchString(id) {
			return fmt.Errorf("%q is not a hands-on exam attempt id", id)
		}
		path = "/" + url.PathEscape(id) + rest
	}
	send := func(creds *Credentials) error {
		var rd *bytes.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				return err
			}
			rd = bytes.NewReader(data)
		}
		var req *http.Request
		var err error
		if rd != nil {
			req, err = http.NewRequestWithContext(ctx, method, creds.Site+"/api/cli/lab-exams"+path, rd)
		} else {
			req, err = http.NewRequestWithContext(ctx, method, creds.Site+"/api/cli/lab-exams"+path, nil)
		}
		if err != nil {
			return err
		}
		if rd != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		// send signs it (Astrona-Proof) when this computer has a device key.
		_, err = c.send(req, creds, out)
		return err
	}
	err := send(cr)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			if errors.Is(rerr, ErrSignedOut) {
				return rerr
			}
			return fmt.Errorf("%s didn't accept your sign-in and it couldn't be renewed: %w", cr.Site, rerr)
		}
		err = send(renewed)
	}
	if err == nil {
		return nil
	}
	if IsProofError(err) {
		// Renewing would not help: this computer's key doesn't match.
		return err
	}
	if !errors.As(err, &he) {
		return fmt.Errorf("could not reach %s to %s: %w", cr.Site, what, err)
	}
	// Labs answers {detail: {code, message}}; the code picks the error.
	switch he.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusNotFound:
		switch he.Code {
		case "unknown_exam":
			return withMessage(ErrExamUnavailable, he.Message)
		case "unknown_task":
			return withMessage(ErrExamUnknownTask, he.Message)
		}
		return withMessage(ErrExamGone, he.Message)
	case http.StatusConflict:
		switch he.Code {
		case "break_limit":
			return withMessage(ErrBreakLimit, he.Message)
		case "open_attempt":
			return withMessage(ErrExamOpen, he.Message)
		case "after_end":
			return withMessage(ErrExamAfterEnd, he.Message)
		case "on_break":
			return withMessage(fmt.Errorf("%w: %w", ErrExamBreakResult, ErrExamState), he.Message)
		}
		return withMessage(ErrExamState, he.Message)
	case http.StatusGone:
		return withMessage(ErrExamPastDeadline, he.Message)
	case http.StatusUnprocessableEntity:
		return withMessage(ErrExamUnavailable, he.Message)
	default:
		return fmt.Errorf("%s couldn't %s (%w)", cr.Site, what, he)
	}
}
