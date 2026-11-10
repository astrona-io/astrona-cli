package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrTooManySessions: the site refused a new lab session (HTTP 429).
	ErrTooManySessions = errors.New("too many lab sessions")
	// ErrUnknownLab: the site doesn't accept the lab name (HTTP 422).
	ErrUnknownLab = errors.New("the lab name was rejected")
)

// LabSession is POST {site}/api/cli/lab-sessions's answer. Token is a
// secret; URL is the lab page to open (it starts the clock).
type LabSession struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	Lab       string `json:"lab"`
	Username  string `json:"username"`
	ExpiresAt string `json:"expires_at"`
	URL       string `json:"url"`
	// MaxMinutes is the attempt's time limit once the lab page opens (0:
	// the site didn't say). A playground's clock is already running.
	MaxMinutes int `json:"max_minutes,omitempty"`
	// Kind is "lab" or "playground"; DeadlineAt is when a playground's
	// clock stops (RFC 3339), set at once because it starts with the run.
	Kind       string `json:"kind,omitempty"`
	DeadlineAt string `json:"deadline_at,omitempty"`
}

// SessionOptions makes a session a playground: its clock starts now and
// stops at astrona destroy or after TimeLimitMinutes (0: the site's default).
//
// Adopt, when set, takes over the session with that id instead of creating
// one: the website created it for this device and `astrona connect` was
// sent `run` for it. Its token comes in the answer, never down the socket.
type SessionOptions struct {
	Kind             string
	TimeLimitMinutes int
	Adopt            string
}

// CreateLabSession asks cr's site for a session for the catalog lab lab.
// A rejected access token is renewed once and the request retried; if the
// site still rejects it, the error wraps ErrSignedOut. 429 wraps
// ErrTooManySessions and 422 ErrUnknownLab, with the site's message.
func (c *Client) CreateLabSession(ctx context.Context, s Store, cr *Credentials, lab string, opts ...SessionOptions) (*LabSession, error) {
	var opt SessionOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	ls, err := c.postLabSession(ctx, cr, lab, opt)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			if errors.Is(rerr, ErrSignedOut) {
				return nil, rerr
			}
			return nil, fmt.Errorf("%s didn't accept your sign-in and it couldn't be renewed: %w", cr.Site, rerr)
		}
		ls, err = c.postLabSession(ctx, renewed, lab, opt)
	}
	if err == nil {
		return ls, nil
	}
	if errors.Is(err, ErrSessionGone) {
		return nil, err
	}
	if !errors.As(err, &he) {
		return nil, fmt.Errorf("could not reach %s to start a lab session: %w", cr.Site, err)
	}
	switch he.Status {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusTooManyRequests:
		return nil, withMessage(ErrTooManySessions, he.Message)
	case http.StatusUnprocessableEntity:
		return nil, withMessage(ErrUnknownLab, he.Message)
	default:
		return nil, fmt.Errorf("%s couldn't start a lab session (%w)", cr.Site, he)
	}
}

func withMessage(err error, msg string) error {
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, msg)
}

func (c *Client) postLabSession(ctx context.Context, cr *Credentials, lab string, opt SessionOptions) (*LabSession, error) {
	if opt.Adopt != "" {
		return c.adoptLabSession(ctx, cr, lab, opt.Adopt)
	}
	payload := map[string]any{"lab": lab}
	if opt.Kind != "" {
		payload["kind"] = opt.Kind
	}
	if opt.TimeLimitMinutes > 0 {
		payload["time_limit_minutes"] = opt.TimeLimitMinutes
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cr.Site+"/api/cli/lab-sessions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var ls LabSession
	if _, err := c.send(req, cr, &ls); err != nil {
		return nil, err
	}
	if ls.URL == "" {
		return nil, fmt.Errorf("%s started a lab session but sent no lab page URL", cr.Site)
	}
	return &ls, nil
}

// adoptLabSession is POST {site}/api/cli/lab-sessions/{id}/adopt. Only the
// device the session was created for may adopt it, and only for the lab it
// is for; anything else is refused here before it is run.
func (c *Client) adoptLabSession(ctx context.Context, cr *Credentials, lab, id string) (*LabSession, error) {
	if !uuidPattern.MatchString(id) {
		return nil, fmt.Errorf("%q is not a lab session id", id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cr.Site+"/api/cli/lab-sessions/"+id+"/adopt", nil)
	if err != nil {
		return nil, err
	}
	var ls LabSession
	if _, err := c.send(req, cr, &ls); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			return nil, withMessage(ErrSessionGone, he.Message)
		}
		return nil, err
	}
	if ls.ID != id || ls.Lab != lab {
		return nil, fmt.Errorf("%s answered for another lab session than %s", cr.Site, id)
	}
	if ls.URL == "" {
		return nil, fmt.Errorf("%s sent no lab page URL for the session", cr.Site)
	}
	return &ls, nil
}

// uuidPattern is a lab session id as the site issues them.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	// ErrSessionGone: the site doesn't know the lab session, or it isn't
	// this account's (HTTP 404) — e.g. it expired.
	ErrSessionGone = errors.New("the lab session has expired or isn't yours")
	// ErrAttemptFinished: the session's attempt is over (HTTP 409) — it
	// passed, or the site keeps one result per attempt (sites before
	// several submits per attempt).
	ErrAttemptFinished = errors.New("this lab attempt is already finished")
	// ErrSessionExpired: the session's time ran out (HTTP 410).
	ErrSessionExpired = errors.New("this lab session has expired")
	// ErrTooManyResults: the attempt holds as many results as the site
	// keeps (HTTP 429 on the results endpoint).
	ErrTooManyResults = errors.New("this lab attempt has too many results")
)

// ResultPost is one graded result for a lab session.
type ResultPost struct {
	// Result is what `astrona submit -o json` prints (any JSON value).
	Result any
	// Usage, when not nil, is the CLI's summary of what the lab used.
	Usage *UsageReport
	// Key identifies this submit: a retry with the same key gets the
	// stored result back instead of a second one. Empty sends none.
	Key string
	// At, when not zero, is when it was graded (a result sent late).
	At time.Time
}

// LabResult is POST {site}/api/cli/lab-sessions/{id}/results's answer.
type LabResult struct {
	ID         string `json:"id"`
	Attempt    int    `json:"attempt"`
	Passed     bool   `json:"passed"`
	FinishedAt string `json:"finished_at"`
	URL        string `json:"url"`
}

// SendLabResult posts a graded result (what `astrona submit -o json`
// prints) to the lab session sessionID on cr's site. A rejected access
// token is renewed once and the request retried, as for CreateLabSession.
// 404 wraps ErrSessionGone, 409 ErrAttemptFinished and 410
// ErrSessionExpired (both with the site's message); any other failure is
// returned as is.
//
// usage, when not nil, is the CLI's summary of what the lab used.
func (c *Client) SendLabResult(ctx context.Context, s Store, cr *Credentials, sessionID string, result any, usage *UsageReport) (*LabResult, error) {
	return c.PostLabResult(ctx, s, cr, sessionID, ResultPost{Result: result, Usage: usage})
}

// PostLabResult is SendLabResult with the submit's idempotency key and
// grading time. The site answers a key it already holds with the stored
// result. 429 wraps ErrTooManyResults.
func (c *Client) PostLabResult(ctx context.Context, s Store, cr *Credentials, sessionID string, p ResultPost) (*LabResult, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return nil, fmt.Errorf("invalid lab session id %q", sessionID)
	}
	lr, err := c.postLabResult(ctx, cr, sessionID, p)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			if errors.Is(rerr, ErrSignedOut) {
				return nil, rerr
			}
			return nil, fmt.Errorf("%s didn't accept your sign-in and it couldn't be renewed: %w", cr.Site, rerr)
		}
		lr, err = c.postLabResult(ctx, renewed, sessionID, p)
	}
	if err == nil {
		return lr, nil
	}
	if !errors.As(err, &he) {
		return nil, fmt.Errorf("could not reach %s to send the result: %w", cr.Site, err)
	}
	switch he.Status {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusNotFound:
		return nil, ErrSessionGone
	case http.StatusConflict:
		return nil, withMessage(ErrAttemptFinished, he.Message)
	case http.StatusGone:
		return nil, withMessage(ErrSessionExpired, he.Message)
	case http.StatusTooManyRequests:
		return nil, withMessage(ErrTooManyResults, he.Message)
	default:
		return nil, fmt.Errorf("%s couldn't record the result (%w)", cr.Site, he)
	}
}

func (c *Client) postLabResult(ctx context.Context, cr *Credentials, sessionID string, p ResultPost) (*LabResult, error) {
	payload := map[string]any{"result": p.Result}
	if p.Usage != nil {
		payload["usage"] = p.Usage
	}
	if p.Key != "" {
		payload["idempotency_key"] = p.Key
	}
	if !p.At.IsZero() {
		payload["at"] = p.At.UTC().Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/results"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var lr LabResult
	if _, err := c.send(req, cr, &lr); err != nil {
		return nil, err
	}
	lr.URL = sanitize(lr.URL)
	return &lr, nil
}

// UpdateLabSessionProgress records a short, safe lifecycle milestone for the
// learner's course page. Detailed local diagnostics never leave the machine.
func (c *Client) UpdateLabSessionProgress(ctx context.Context, cr *Credentials, sessionID, phase, message string) error {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return fmt.Errorf("invalid lab session id %q", sessionID)
	}
	body, err := json.Marshal(map[string]string{"phase": phase, "message": message})
	if err != nil {
		return err
	}
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/progress"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if _, err := c.send(req, cr, nil); err != nil {
		return err
	}
	return nil
}

// StopLabSession stops a playground's clock: reason is "destroyed"
// (astrona destroy) or "time_limit" (the watchdog at the limit). The site
// keeps the first stop, so a second one is harmless. A rejected access
// token is renewed once; a session the site no longer knows wraps
// ErrSessionGone. usage, when not nil, is the CLI's summary of what the
// playground used; at, when not zero, is when it stopped (a stop kept while
// offline).
func (c *Client) StopLabSession(ctx context.Context, s Store, cr *Credentials, sessionID, reason string, usage *UsageReport, at time.Time) error {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return fmt.Errorf("invalid lab session id %q", sessionID)
	}
	err := c.postStop(ctx, cr, sessionID, reason, usage, at)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return rerr
		}
		err = c.postStop(ctx, renewed, sessionID, reason, usage, at)
	}
	if err == nil {
		return nil
	}
	if !errors.As(err, &he) {
		return fmt.Errorf("could not reach %s to stop the playground's clock: %w", cr.Site, err)
	}
	switch he.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusNotFound:
		return withMessage(ErrSessionGone, he.Message)
	default:
		return fmt.Errorf("%s couldn't stop the playground's clock (%w)", cr.Site, he)
	}
}

func (c *Client) postStop(ctx context.Context, cr *Credentials, sessionID, reason string, usage *UsageReport, at time.Time) error {
	payload := map[string]any{"reason": reason}
	if usage != nil {
		payload["usage"] = usage
	}
	if !at.IsZero() {
		payload["at"] = at.UTC().Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/stop"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var out map[string]any
	_, err = c.send(req, cr, &out)
	return err
}

// StartLabSession starts a lab session's clock on the site: the learner
// pressed Start in the browser workspace (or the clock started at readiness
// outside it), so the website lab page never claims the session. at, when not
// zero, is when the clock started. The site sets the start only once and
// returns a finished session unchanged, so a repeat is harmless. A rejected
// access token is renewed once; a session the site doesn't know (or that
// isn't yours) wraps ErrSessionGone.
func (c *Client) StartLabSession(ctx context.Context, s Store, cr *Credentials, sessionID string, at time.Time) error {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return fmt.Errorf("invalid lab session id %q", sessionID)
	}
	err := c.postStart(ctx, cr, sessionID, at)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return rerr
		}
		err = c.postStart(ctx, renewed, sessionID, at)
	}
	if err == nil {
		return nil
	}
	if !errors.As(err, &he) {
		return fmt.Errorf("could not reach %s to start the lab's clock: %w", cr.Site, err)
	}
	switch he.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusNotFound:
		return withMessage(ErrSessionGone, he.Message)
	default:
		return fmt.Errorf("%s couldn't start the lab's clock (%w)", cr.Site, he)
	}
}

func (c *Client) postStart(ctx context.Context, cr *Credentials, sessionID string, at time.Time) error {
	payload := map[string]any{}
	if !at.IsZero() {
		payload["at"] = at.UTC().Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/start"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var out map[string]any
	_, err = c.send(req, cr, &out)
	return err
}

// RenewedSession is POST {site}/api/cli/lab-sessions/{id}/renew's answer.
// The site keeps the session open and restarts its time limit, up to
// DeadlineAt (RFC 3339): ID is PreviousID and BankedSeconds is 0, and the
// time counts once, when the playground ends. An older site closed
// PreviousID like a destroy (BankedSeconds counted as playground time) and
// answered with a new ID; the CLI accepts both.
type RenewedSession struct {
	PreviousID    string `json:"previous_id"`
	BankedSeconds int    `json:"banked_seconds"`
	ID            string `json:"id"`
	ExpiresAt     string `json:"expires_at"`
	DeadlineAt    string `json:"deadline_at"`
	MaxMinutes    int    `json:"max_minutes"`
}

// ErrPlaygroundStopped: the playground's clock already stopped (destroyed,
// or past its time limit) — it can't be renewed, only run again.
var ErrPlaygroundStopped = errors.New("this playground has already stopped")

// RenewLabSession restarts a running playground's timer (`astrona run
// renew`): the same session goes on with a full time limit again. A
// rejected access token is
// renewed once; 404 wraps ErrSessionGone and 409
// ErrPlaygroundStopped.
func (c *Client) RenewLabSession(ctx context.Context, s Store, cr *Credentials, sessionID string) (*RenewedSession, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return nil, fmt.Errorf("invalid lab session id %q", sessionID)
	}
	out, err := c.postRenew(ctx, cr, sessionID)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return nil, rerr
		}
		out, err = c.postRenew(ctx, renewed, sessionID)
	}
	if err == nil {
		return out, nil
	}
	if !errors.As(err, &he) {
		return nil, fmt.Errorf("could not reach %s to renew the playground: %w", cr.Site, err)
	}
	switch he.Status {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusNotFound:
		return nil, withMessage(ErrSessionGone, he.Message)
	case http.StatusConflict:
		return nil, withMessage(ErrPlaygroundStopped, he.Message)
	default:
		return nil, fmt.Errorf("%s couldn't renew the playground (%w)", cr.Site, he)
	}
}

func (c *Client) postRenew(ctx context.Context, cr *Credentials, sessionID string) (*RenewedSession, error) {
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/renew"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var out RenewedSession
	if _, err := c.send(req, cr, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
