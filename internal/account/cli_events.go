package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// PostCLIEvents sends one batch of usage events (internal/telemetry) to
// {site}/api/cli/events. With cr it is signed in (bearer and proof, a
// rejected access token renewed once); with cr nil it carries no sign-in
// and the site stores it with the random install id only.
func (c *Client) PostCLIEvents(ctx context.Context, s Store, cr *Credentials, site, installID string, events any) error {
	body, err := json.Marshal(map[string]any{"install_id": installID, "events": events})
	if err != nil {
		return err
	}
	if cr == nil {
		req, err := c.cliEventsRequest(ctx, site, body)
		if err != nil {
			return err
		}
		_, err = do(c.HTTP, req, nil)
		return err
	}
	err = c.postSignedEvents(ctx, cr, body)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return rerr
		}
		err = c.postSignedEvents(ctx, renewed, body)
	}
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		return fmt.Errorf("the Astrona site %s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
	}
	return err
}

func (c *Client) cliEventsRequest(ctx context.Context, site string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(site, "/")+"/api/cli/events", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) postSignedEvents(ctx context.Context, cr *Credentials, body []byte) error {
	req, err := c.cliEventsRequest(ctx, cr.Site, body)
	if err != nil {
		return err
	}
	_, err = c.send(req, cr, nil)
	return err
}
