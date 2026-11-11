package account

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// PostGuestRun sends one signed-out run's anonymous report to
// {site}/api/cli/guest-runs. It carries no sign-in: guests have none. body
// is the report as the site takes it (internal/guestrun).
func (c *Client) PostGuestRun(ctx context.Context, site string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(site, "/")+"/api/cli/guest-runs", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = do(c.HTTP, req, nil)
	return err
}
