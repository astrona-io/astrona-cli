package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
