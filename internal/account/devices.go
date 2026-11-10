package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DeviceInfo is one computer signed in to the account, as
// GET {site}/api/cli/devices lists it for `astrona devices`.
type DeviceInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"device_name"`
	OS         string     `json:"os"`
	Arch       string     `json:"arch,omitempty"`
	CLIVersion string     `json:"cli_version,omitempty"`
	Engine     string     `json:"engine,omitempty"`
	Capacity   *Capacity  `json:"capacity,omitempty"`
	IsDefault  bool       `json:"is_default"`
	Online     bool       `json:"online"`
	Current    bool       `json:"current"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt time.Time  `json:"last_used_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// Capacity is what a device can give a lab, as `astrona connect` reports it.
type Capacity struct {
	CPUs          int   `json:"cpus,omitempty"`
	MemoryBytes   int64 `json:"memory_bytes,omitempty"`
	DiskFreeBytes int64 `json:"disk_free_bytes,omitempty"`
}

// ListDevices returns the account's signed-in devices, this one marked
// Current. A rejected access token is renewed once and the request retried.
func (c *Client) ListDevices(ctx context.Context, s Store, cr *Credentials) ([]DeviceInfo, error) {
	list, err := c.getDevices(ctx, cr)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
		renewed, rerr := c.Renew(ctx, s, cr)
		if rerr != nil {
			return nil, rerr
		}
		list, err = c.getDevices(ctx, renewed)
	}
	if err != nil {
		if errors.As(err, &he) && he.Status == http.StatusUnauthorized {
			return nil, fmt.Errorf("%s didn't accept your sign-in: %w", cr.Site, ErrSignedOut)
		}
		return nil, fmt.Errorf("could not list your devices on %s: %w", cr.Site, err)
	}
	return list, nil
}

func (c *Client) getDevices(ctx context.Context, cr *Credentials) ([]DeviceInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cr.Site+"/api/cli/devices", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	var list []DeviceInfo
	if _, err := do(c.HTTP, req, &list); err != nil {
		return nil, err
	}
	return list, nil
}
