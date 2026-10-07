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
	// the site didn't say).
	MaxMinutes int `json:"max_minutes,omitempty"`
}

// CreateLabSession asks cr's site for a session for the catalog lab lab.
// A rejected access token is renewed once and the request retried; if the
// site still rejects it, the error wraps ErrSignedOut. 429 wraps
// ErrTooManySessions and 422 ErrUnknownLab, with the site's message.
func (c *Client) CreateLabSession(ctx context.Context, s Store, cr *Credentials, lab string) (*LabSession, error) {
	ls, err := c.postLabSession(ctx, cr, lab)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			if errors.Is(rerr, ErrSignedOut) {
				return nil, rerr
			}
			return nil, fmt.Errorf("%s didn't accept your sign-in and it couldn't be renewed: %w", cr.Site, rerr)
		}
		ls, err = c.postLabSession(ctx, renewed, lab)
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

func (c *Client) postLabSession(ctx context.Context, cr *Credentials, lab string) (*LabSession, error) {
	body, err := json.Marshal(map[string]string{"lab": lab})
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
