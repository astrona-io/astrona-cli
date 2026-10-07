package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrSignedOut: there are no usable credentials — never signed in,
	// signed out, signed in to another site, saved by an older astrona, or
	// the sign-in was revoked or expired. `astrona login` is the fix.
	ErrSignedOut = errors.New("not signed in")
	// ErrAccessDenied: the student declined the sign-in in the browser.
	ErrAccessDenied = errors.New("sign-in was declined in the browser")
	// ErrDeviceCodeExpired: the code wasn't authorized in time.
	ErrDeviceCodeExpired = errors.New("the sign-in code expired before it was authorized")
)

// tokenPattern is what a bearer token may look like (RFC 6750 b64token) —
// anything else is refused before it is stored or put in a header.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

const (
	// maxDeviceName is how much of the hostname is sent as the device name.
	maxDeviceName = 100
	// maxInterval caps the poll interval the site asks for (and slow_down).
	maxInterval = 60 * time.Second
	// maxCodeLifetime caps how long astrona waits for the browser.
	maxCodeLifetime = 30 * time.Minute
)

// Client talks to the site. The zero value is not usable; call NewClient.
type Client struct {
	HTTP *http.Client
	Now  func() time.Time
	// Sleep waits d or until ctx is done (overridden in tests).
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewClient returns a Client with the package's HTTP defaults.
func NewClient() *Client {
	return &Client{HTTP: newHTTPClient(), Now: time.Now, Sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SiteConfig is GET {site}/api/cli/config: where the CLI signs in. The
// site answers with site-relative paths; FetchSiteConfig turns them into
// absolute URLs on the site itself.
type SiteConfig struct {
	DeviceEndpoint     string `json:"device_endpoint"`
	TokenEndpoint      string `json:"token_endpoint"`
	RevocationEndpoint string `json:"revocation_endpoint"`
}

// FetchSiteConfig asks site where to start a sign-in, poll for it and
// revoke it. Every endpoint must resolve to the site itself (same scheme and
// host) — tokens are never sent anywhere else.
func (c *Client) FetchSiteConfig(ctx context.Context, site string) (SiteConfig, error) {
	var sc SiteConfig
	if err := getJSON(ctx, c.HTTP, site+"/api/cli/config", &sc); err != nil {
		return sc, fmt.Errorf("could not read %s's sign-in settings: %w", site, err)
	}
	for _, e := range []struct {
		name     string
		value    *string
		required bool
	}{
		{"device_endpoint", &sc.DeviceEndpoint, true},
		{"token_endpoint", &sc.TokenEndpoint, true},
		{"revocation_endpoint", &sc.RevocationEndpoint, false},
	} {
		if *e.value == "" && !e.required {
			continue
		}
		abs, err := resolveOnSite(site, *e.value)
		if err != nil {
			return sc, fmt.Errorf("%s's sign-in settings: %s %w", site, e.name, err)
		}
		*e.value = abs
	}
	return sc, nil
}

// resolveOnSite resolves an endpoint path against site and refuses anything
// that lands on another scheme or host ("//evil.example", "https://…").
func resolveOnSite(site, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("is missing")
	}
	base, err := checkTokenURL(site)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("%q is not a valid path: %w", sanitize(ref), err)
	}
	u := base.ResolveReference(r)
	if u.User != nil || !sameOrigin(u, base) {
		return "", fmt.Errorf("%q is not on %s — refusing to use it", sanitize(ref), base.Host)
	}
	u.Fragment = ""
	return u.String(), nil
}

// Device describes this computer to the site, which lists it among the
// student's signed-in devices.
type Device struct {
	Name string `json:"device_name"`
	OS   string `json:"os"`
}

// DeviceName is the name sent for this computer: its hostname, printable
// and at most 100 characters.
func DeviceName(hostname string) string {
	name := []rune(sanitize(hostname))
	if len(name) > maxDeviceName {
		name = name[:maxDeviceName]
	}
	if s := strings.TrimSpace(string(name)); s != "" {
		return s
	}
	return "unknown"
}

// DeviceCode is the device endpoint's answer.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// StartDeviceFlow asks the site for a device code and the page where the
// student authorizes it. That page must be on the site.
func (c *Client) StartDeviceFlow(ctx context.Context, site string, sc SiteConfig, d Device) (DeviceCode, error) {
	var dc DeviceCode
	if _, err := postJSON(ctx, c.HTTP, sc.DeviceEndpoint, d, &dc); err != nil {
		return dc, fmt.Errorf("could not start the sign-in on %s: %w", site, err)
	}
	if !tokenPattern.MatchString(dc.DeviceCode) || sanitize(dc.UserCode) == "" {
		return dc, fmt.Errorf("%s's answer has no usable device or user code", site)
	}
	dc.UserCode = sanitize(dc.UserCode)
	page, err := CheckHTTPURL(dc.VerificationURIComplete)
	if err != nil {
		return dc, fmt.Errorf("%s gave an unusable sign-in page: %w", site, err)
	}
	if !SameSite(page, site) {
		return dc, fmt.Errorf("%s sent a sign-in page on another site (%s) — refusing to open it", site, page.Host)
	}
	dc.VerificationURIComplete = page.String()
	if u, err := CheckHTTPURL(dc.VerificationURI); err != nil || !SameSite(u, site) {
		dc.VerificationURI = ""
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	if dc.ExpiresIn <= 0 {
		dc.ExpiresIn = 600
	}
	return dc, nil
}

// Tokens is the token endpoint's answer. AccessToken and RefreshToken are
// opaque secrets issued by the site.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Username     string `json:"username"`
	DeviceID     string `json:"device_id"`
}

func (t *Tokens) check() error {
	if !tokenPattern.MatchString(t.AccessToken) {
		return fmt.Errorf("the site returned no usable access token")
	}
	if !tokenPattern.MatchString(t.RefreshToken) {
		return fmt.Errorf("the site returned no usable refresh token")
	}
	if t.TokenType != "" && !strings.EqualFold(t.TokenType, "bearer") {
		return fmt.Errorf("the site returned a %q token, not a bearer token", sanitize(t.TokenType))
	}
	t.Username = sanitize(t.Username)
	t.DeviceID = sanitize(t.DeviceID)
	return nil
}

type deviceGrant struct {
	GrantType  string `json:"grant_type"`
	DeviceCode string `json:"device_code"`
}

type refreshGrant struct {
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
}

// PollToken polls the token endpoint until the student authorizes the
// device in the browser, honouring interval and slow_down. It stops with
// ErrAccessDenied, ErrDeviceCodeExpired, or ctx's error (Ctrl+C).
func (c *Client) PollToken(ctx context.Context, sc SiteConfig, dc DeviceCode) (Tokens, error) {
	interval := min(time.Duration(dc.Interval)*time.Second, maxInterval)
	deadline := c.Now().Add(min(time.Duration(dc.ExpiresIn)*time.Second, maxCodeLifetime))
	grant := deviceGrant{GrantType: "device_code", DeviceCode: dc.DeviceCode}
	netErrors := 0
	for {
		if err := c.Sleep(ctx, interval); err != nil {
			return Tokens{}, err
		}
		if c.Now().After(deadline) {
			return Tokens{}, ErrDeviceCodeExpired
		}
		var t Tokens
		_, err := postJSON(ctx, c.HTTP, sc.TokenEndpoint, grant, &t)
		if err == nil {
			return t, t.check()
		}
		if ctx.Err() != nil {
			return Tokens{}, ctx.Err()
		}
		var he *httpError
		if !errors.As(err, &he) {
			// A dropped connection while the student is in the browser:
			// keep polling, but not forever.
			if netErrors++; netErrors >= 3 {
				return Tokens{}, fmt.Errorf("waiting for the sign-in: %w", err)
			}
			continue
		}
		netErrors = 0
		switch he.Code {
		case "authorization_pending":
		case "slow_down":
			interval = min(interval+5*time.Second, maxInterval)
		case "access_denied":
			return Tokens{}, ErrAccessDenied
		case "expired_token":
			return Tokens{}, ErrDeviceCodeExpired
		default:
			return Tokens{}, fmt.Errorf("the sign-in failed: %w", err)
		}
	}
}

// refresh exchanges cr's refresh token for new tokens (the refresh token
// rotates: the old one stops working). An invalid_grant (expired, revoked,
// or already used) is ErrSignedOut.
func (c *Client) refresh(ctx context.Context, cr *Credentials) (Tokens, error) {
	var t Tokens
	sc, err := c.FetchSiteConfig(ctx, cr.Site)
	if err != nil {
		return t, fmt.Errorf("could not renew your sign-in: %w", err)
	}
	grant := refreshGrant{GrantType: "refresh_token", RefreshToken: cr.RefreshToken}
	if _, err := postJSON(ctx, c.HTTP, sc.TokenEndpoint, grant, &t); err != nil {
		var he *httpError
		if errors.As(err, &he) && (he.Code == "invalid_grant" || he.Code == "invalid_token") {
			return t, fmt.Errorf("your sign-in has expired or was revoked: %w", ErrSignedOut)
		}
		return t, fmt.Errorf("could not renew your sign-in: %w", err)
	}
	return t, t.check()
}

// Revoke revokes refreshToken (and with it this device's sign-in) on site.
func (c *Client) Revoke(ctx context.Context, site, refreshToken string) error {
	sc, err := c.FetchSiteConfig(ctx, site)
	if err != nil {
		return err
	}
	if sc.RevocationEndpoint == "" {
		return fmt.Errorf("%s has no revocation endpoint", site)
	}
	body := struct {
		RefreshToken string `json:"refresh_token"`
	}{refreshToken}
	_, err = postJSON(ctx, c.HTTP, sc.RevocationEndpoint, body, nil)
	return err
}
