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
	"time"
)

// UsageSample is what a running lab's cluster or VMs used at At: CPU in
// cores (1.0 is one core fully busy, so 250% in `docker stats` is 2.5) and
// resident memory in bytes.
type UsageSample struct {
	At          time.Time `json:"at"`
	CPUCores    float64   `json:"cpu_cores"`
	MemoryBytes int64     `json:"memory_bytes"`
}

// MaxUsageSamplesPerPost is the most samples the site takes in one post.
const MaxUsageSamplesPerPost = 40

// UsageReport is the CLI's own summary of a run, sent with `astrona submit`
// and `astrona destroy`. The site uses it only when no sample reached it.
type UsageReport struct {
	AverageCPUCores    float64 `json:"average_cpu_cores"`
	AverageMemoryBytes int64   `json:"average_memory_bytes"`
	PeakCPUCores       float64 `json:"peak_cpu_cores,omitempty"`
	PeakMemoryBytes    int64   `json:"peak_memory_bytes,omitempty"`
	Samples            uint64  `json:"samples"`
}

// SendLabUsage posts resource samples for the lab session sessionID and
// returns how many the site kept (it drops repeats and samples outside the
// run). A rejected access token is renewed once; 404 wraps ErrSessionGone.
func (c *Client) SendLabUsage(ctx context.Context, s Store, cr *Credentials, sessionID string, samples []UsageSample) (int, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return 0, fmt.Errorf("invalid lab session id %q", sessionID)
	}
	if len(samples) == 0 {
		return 0, nil
	}
	if len(samples) > MaxUsageSamplesPerPost {
		samples = samples[len(samples)-MaxUsageSamplesPerPost:]
	}
	kept, err := c.postUsage(ctx, cr, sessionID, samples)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return 0, rerr
		}
		kept, err = c.postUsage(ctx, renewed, sessionID, samples)
	}
	if err == nil || !errors.As(err, &he) {
		return kept, err
	}
	switch he.Status {
	case http.StatusUnauthorized:
		return 0, fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusNotFound:
		return 0, withMessage(ErrSessionGone, he.Message)
	default:
		return 0, fmt.Errorf("%s couldn't record the lab's usage (%w)", cr.Site, he)
	}
}

func (c *Client) postUsage(ctx context.Context, cr *Credentials, sessionID string, samples []UsageSample) (int, error) {
	body, err := json.Marshal(map[string]any{"samples": samples})
	if err != nil {
		return 0, err
	}
	endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/usage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	var out struct {
		Accepted int `json:"accepted"`
	}
	if _, err := do(c.HTTP, req, &out); err != nil {
		return 0, err
	}
	return out.Accepted, nil
}

// PauseLabSession tells the site the lab window went idle or closed at at:
// the run's clock stops counting. resume ends the pause instead. Both are
// idempotent on the site. A rejected access token is renewed once; 404 wraps
// ErrSessionGone.
func (c *Client) PauseLabSession(ctx context.Context, s Store, cr *Credentials, sessionID string, resume bool, at time.Time) error {
	if sessionID == "" || strings.ContainsAny(sessionID, "/?#") {
		return fmt.Errorf("invalid lab session id %q", sessionID)
	}
	kind := "pause"
	if resume {
		kind = "resume"
	}
	post := func(cr *Credentials) error {
		body, err := json.Marshal(map[string]string{"at": at.UTC().Format(time.RFC3339Nano)})
		if err != nil {
			return err
		}
		endpoint := cr.Site + "/api/cli/lab-sessions/" + url.PathEscape(sessionID) + "/" + kind
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
	err := post(cr)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return rerr
		}
		err = post(renewed)
	}
	if err == nil || !errors.As(err, &he) {
		return err
	}
	switch he.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	case http.StatusNotFound:
		return withMessage(ErrSessionGone, he.Message)
	default:
		return fmt.Errorf("%s couldn't %s the lab's clock (%w)", cr.Site, kind, he)
	}
}

// Permanent reports whether the site refused a request for good — a session it
// no longer knows, or another 4xx answer than 401, 408 or 429 — so sending it
// again would not help.
func Permanent(err error) bool {
	if errors.Is(err, ErrSessionGone) {
		return true
	}
	var he *httpError
	if !errors.As(err, &he) {
		return false
	}
	switch he.Status {
	case http.StatusUnauthorized, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return he.Status >= 400 && he.Status < 500
}
