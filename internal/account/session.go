package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
type SessionOptions struct {
	Kind             string
	TimeLimitMinutes int
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
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	var ls LabSession
	if _, err := do(c.HTTP, req, &ls); err != nil {
		return nil, err
	}
	if ls.URL == "" {
		return nil, fmt.Errorf("%s started a lab session but sent no lab page URL", cr.Site)
	}
	return &ls, nil
}

var (
	// ErrSessionGone: the site doesn't know the lab session, or it isn't
	// this account's (HTTP 404) — e.g. it expired.
	ErrSessionGone = errors.New("the lab session has expired or isn't yours")
	// ErrAttemptFinished: the session's attempt already has its result
	// (HTTP 409).
	ErrAttemptFinished = errors.New("this lab attempt is already finished")
	// ErrSessionExpired: the session's time ran out (HTTP 410).
	ErrSessionExpired = errors.New("this lab session has expired")
)

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
func (c *Client) SendLabResult(ctx context.Context, s Store, cr *Credentials, sessionID string, result any) (*LabResult, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return nil, fmt.Errorf("invalid lab session id %q", sessionID)
	}
	lr, err := c.postLabResult(ctx, cr, sessionID, result)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			if errors.Is(rerr, ErrSignedOut) {
				return nil, rerr
			}
			return nil, fmt.Errorf("%s didn't accept your sign-in and it couldn't be renewed: %w", cr.Site, rerr)
		}
		lr, err = c.postLabResult(ctx, renewed, sessionID, result)
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
	default:
		return nil, fmt.Errorf("%s couldn't record the result (%w)", cr.Site, he)
	}
}

func (c *Client) postLabResult(ctx context.Context, cr *Credentials, sessionID string, result any) (*LabResult, error) {
	body, err := json.Marshal(map[string]any{"result": result})
	if err != nil {
		return nil, err
	}
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/results"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	var lr LabResult
	if _, err := do(c.HTTP, req, &lr); err != nil {
		return nil, err
	}
	lr.URL = sanitize(lr.URL)
	return &lr, nil
}

// StopLabSession stops a playground's clock: reason is "destroyed"
// (astrona destroy) or "time_limit" (the watchdog at the limit). The site
// keeps the first stop, so a second one is harmless. A rejected access
// token is renewed once; a session the site no longer knows wraps
// ErrSessionGone.
func (c *Client) StopLabSession(ctx context.Context, s Store, cr *Credentials, sessionID, reason string) error {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return fmt.Errorf("invalid lab session id %q", sessionID)
	}
	err := c.postStop(ctx, cr, sessionID, reason)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return rerr
		}
		err = c.postStop(ctx, renewed, sessionID, reason)
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

func (c *Client) postStop(ctx context.Context, cr *Credentials, sessionID, reason string) error {
	body, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		return err
	}
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/stop"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	var out map[string]any
	_, err = do(c.HTTP, req, &out)
	return err
}

// RenewedSession is POST {site}/api/cli/lab-sessions/{id}/renew's answer.
// The site closed PreviousID like a destroy — its BankedSeconds count as
// playground time — and started ID for the same playground, with a full
// time limit up to DeadlineAt (RFC 3339).
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
// renew`): the site banks the session so far, as a destroy would, and
// starts a new one with a full time limit. A rejected access token is
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
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	var out RenewedSession
	if _, err := do(c.HTTP, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
