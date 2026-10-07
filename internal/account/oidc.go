package account

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Scope is what `astrona login` asks for: an ID token for the username and
// a refresh token that outlives the browser session (offline_access).
const Scope = "openid profile offline_access"

const deviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"

var (
	// ErrSignedOut: there are no usable credentials — never signed in,
	// signed out, signed in to another site, or the sign-in was revoked or
	// expired. `astrona login` is the fix.
	ErrSignedOut = errors.New("not signed in")
	// ErrAccessDenied: the user declined the sign-in in the browser.
	ErrAccessDenied = errors.New("sign-in was denied in the browser")
	// ErrDeviceCodeExpired: the code wasn't confirmed in time.
	ErrDeviceCodeExpired = errors.New("the sign-in code expired before it was confirmed")
)

// tokenPattern is what a bearer token may look like (RFC 6750 b64token) —
// anything else is refused before it is stored or put in a header.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

// clientIDPattern bounds the client_id the site hands out.
var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// Client talks to the site and its issuer. The zero value is not usable;
// call NewClient.
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

// SiteConfig is GET {site}/api/cli/config: where to sign in.
type SiteConfig struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
}

// FetchSiteConfig asks site which Keycloak realm (issuer) and public client
// the CLI signs in with.
func (c *Client) FetchSiteConfig(ctx context.Context, site string) (SiteConfig, error) {
	var sc SiteConfig
	if err := getJSON(ctx, c.HTTP, site+"/api/cli/config", &sc); err != nil {
		return sc, fmt.Errorf("could not read %s's sign-in settings: %w", site, err)
	}
	if _, err := checkTokenURL(sc.Issuer); err != nil {
		return sc, fmt.Errorf("%s gave an unusable sign-in issuer %q: %w", site, sanitize(sc.Issuer), err)
	}
	sc.Issuer = strings.TrimRight(strings.TrimSpace(sc.Issuer), "/")
	if !clientIDPattern.MatchString(sc.ClientID) {
		return sc, fmt.Errorf("%s gave an unusable sign-in client id %q", site, sanitize(sc.ClientID))
	}
	return sc, nil
}

// Provider is the part of the issuer's OpenID configuration the CLI uses.
type Provider struct {
	Issuer                      string `json:"issuer"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	RevocationEndpoint          string `json:"revocation_endpoint"`
	EndSessionEndpoint          string `json:"end_session_endpoint"`
}

// Discover reads issuer's /.well-known/openid-configuration. The document
// must name the same issuer, and every endpoint must be on the issuer's own
// scheme and host — tokens go nowhere else.
func (c *Client) Discover(ctx context.Context, issuer string) (Provider, error) {
	var p Provider
	iss, err := checkTokenURL(issuer)
	if err != nil {
		return p, fmt.Errorf("issuer %q: %w", sanitize(issuer), err)
	}
	if err := getJSON(ctx, c.HTTP, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", &p); err != nil {
		return p, fmt.Errorf("could not read the sign-in server's configuration: %w", err)
	}
	if strings.TrimRight(p.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return p, fmt.Errorf("the sign-in server says it is %q, not %q — refusing to use it", sanitize(p.Issuer), issuer)
	}
	check := func(name, raw string, required bool) error {
		if raw == "" {
			if required {
				return fmt.Errorf("the sign-in server has no %s (is the device flow enabled for the realm?)", name)
			}
			return nil
		}
		u, err := checkTokenURL(raw)
		if err != nil {
			return fmt.Errorf("the sign-in server's %s: %w", name, err)
		}
		if !sameOrigin(u, iss) {
			return fmt.Errorf("the sign-in server's %s %s is not on %s — refusing to use it", name, u.Redacted(), iss.Host)
		}
		return nil
	}
	for _, e := range []struct {
		name, url string
		required  bool
	}{
		{"device_authorization_endpoint", p.DeviceAuthorizationEndpoint, true},
		{"token_endpoint", p.TokenEndpoint, true},
		{"revocation_endpoint", p.RevocationEndpoint, false},
		{"end_session_endpoint", p.EndSessionEndpoint, false},
	} {
		if err := check(e.name, e.url, e.required); err != nil {
			return p, err
		}
	}
	return p, nil
}

// DeviceCode is the device authorization response (RFC 8628 §3.2).
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// StartDeviceFlow asks for a device code and the user code to confirm.
func (c *Client) StartDeviceFlow(ctx context.Context, p Provider, clientID string) (DeviceCode, error) {
	var dc DeviceCode
	form := url.Values{"client_id": {clientID}, "scope": {Scope}}
	if _, err := postForm(ctx, c.HTTP, p.DeviceAuthorizationEndpoint, form, &dc); err != nil {
		return dc, fmt.Errorf("could not start the sign-in: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return dc, fmt.Errorf("the sign-in server's answer has no device or user code")
	}
	dc.UserCode = sanitize(dc.UserCode)
	if _, err := CheckHTTPURL(dc.VerificationURI); err != nil {
		return dc, fmt.Errorf("the sign-in server gave an unusable verification URL: %w", err)
	}
	if dc.VerificationURIComplete != "" {
		if _, err := CheckHTTPURL(dc.VerificationURIComplete); err != nil {
			dc.VerificationURIComplete = "" // fall back to the URL + code
		}
	}
	if dc.Interval <= 0 {
		dc.Interval = 5 // RFC 8628 §3.2 default
	}
	if dc.ExpiresIn <= 0 {
		dc.ExpiresIn = 600
	}
	return dc, nil
}

// Tokens is a token endpoint response.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func (t Tokens) check() error {
	if !tokenPattern.MatchString(t.AccessToken) {
		return fmt.Errorf("the sign-in server returned no usable access token")
	}
	if t.RefreshToken != "" && !tokenPattern.MatchString(t.RefreshToken) {
		return fmt.Errorf("the sign-in server returned an unusable refresh token")
	}
	if t.TokenType != "" && !strings.EqualFold(t.TokenType, "bearer") {
		return fmt.Errorf("the sign-in server returned a %q token, not a bearer token", sanitize(t.TokenType))
	}
	return nil
}

// PollToken polls the token endpoint until the user confirms the code in
// the browser, honouring interval and slow_down (RFC 8628 §3.5). It stops
// with ErrAccessDenied, ErrDeviceCodeExpired, or ctx's error (Ctrl+C).
func (c *Client) PollToken(ctx context.Context, p Provider, clientID string, dc DeviceCode) (Tokens, error) {
	interval := time.Duration(dc.Interval) * time.Second
	deadline := c.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	form := url.Values{"grant_type": {deviceCodeGrant}, "device_code": {dc.DeviceCode}, "client_id": {clientID}}
	netErrors := 0
	for {
		if err := c.Sleep(ctx, interval); err != nil {
			return Tokens{}, err
		}
		if c.Now().After(deadline) {
			return Tokens{}, ErrDeviceCodeExpired
		}
		var t Tokens
		_, err := postForm(ctx, c.HTTP, p.TokenEndpoint, form, &t)
		if err == nil {
			return t, t.check()
		}
		if ctx.Err() != nil {
			return Tokens{}, ctx.Err()
		}
		var he *httpError
		if !errors.As(err, &he) {
			// A dropped connection while the user is in the browser:
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
			interval += 5 * time.Second
		case "access_denied":
			return Tokens{}, ErrAccessDenied
		case "expired_token":
			return Tokens{}, ErrDeviceCodeExpired
		default:
			return Tokens{}, fmt.Errorf("the sign-in failed: %w", err)
		}
	}
}

// refresh exchanges a refresh token for new tokens. An invalid_grant (the
// refresh token expired or was revoked) is ErrSignedOut.
func (c *Client) refresh(ctx context.Context, cr *Credentials) (Tokens, error) {
	var t Tokens
	if cr.RefreshToken == "" {
		return t, fmt.Errorf("your sign-in has expired: %w", ErrSignedOut)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cr.RefreshToken}, "client_id": {cr.ClientID}}
	if _, err := postForm(ctx, c.HTTP, cr.TokenEndpoint, form, &t); err != nil {
		var he *httpError
		if errors.As(err, &he) && (he.Code == "invalid_grant" || he.Code == "invalid_token") {
			return t, fmt.Errorf("your sign-in has expired or was revoked: %w", ErrSignedOut)
		}
		return t, fmt.Errorf("could not renew your sign-in: %w", err)
	}
	return t, t.check()
}

// Revoke revokes token (a refresh token) at endpoint (RFC 7009).
func (c *Client) Revoke(ctx context.Context, endpoint, clientID, token string) error {
	if endpoint == "" {
		return fmt.Errorf("the sign-in server has no revocation endpoint")
	}
	form := url.Values{"token": {token}, "token_type_hint": {"refresh_token"}, "client_id": {clientID}}
	_, err := postForm(ctx, c.HTTP, endpoint, form, nil)
	return err
}

// UsernameFromTokens reads preferred_username from the ID token (or the
// access token). Display only: the payload is decoded, not verified, so it
// must never be used to decide anything.
func UsernameFromTokens(t Tokens) string {
	for _, jwt := range []string{t.IDToken, t.AccessToken} {
		if name := jwtUsername(jwt); name != "" {
			return name
		}
	}
	return ""
}

func jwtUsername(jwt string) string {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if claims.PreferredUsername != "" {
		return sanitize(claims.PreferredUsername)
	}
	return sanitize(claims.Email)
}
