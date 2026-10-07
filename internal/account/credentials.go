package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// expirySkew renews an access token this long before it expires, so it
// doesn't run out between the check and the request.
const expirySkew = 30 * time.Second

// Credentials is what `astrona login` saves. AccessToken and RefreshToken
// are secrets: never print or log them.
type Credentials struct {
	Site               string    `json:"site"`
	Issuer             string    `json:"issuer"`
	ClientID           string    `json:"client_id"`
	TokenEndpoint      string    `json:"token_endpoint"`
	RevocationEndpoint string    `json:"revocation_endpoint,omitempty"`
	AccessToken        string    `json:"access_token"`
	RefreshToken       string    `json:"refresh_token,omitempty"`
	AccessExpiresAt    time.Time `json:"access_expires_at"`
	Username           string    `json:"username,omitempty"`
}

// NewCredentials builds the credentials for tokens obtained from p.
func NewCredentials(site string, sc SiteConfig, p Provider, t Tokens, now time.Time) *Credentials {
	cr := &Credentials{
		Site:               site,
		Issuer:             sc.Issuer,
		ClientID:           sc.ClientID,
		TokenEndpoint:      p.TokenEndpoint,
		RevocationEndpoint: p.RevocationEndpoint,
		Username:           UsernameFromTokens(t),
	}
	cr.apply(t, now)
	return cr
}

// apply stores tokens; a refresh answer without a new refresh token keeps
// the old one.
func (cr *Credentials) apply(t Tokens, now time.Time) {
	cr.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		cr.RefreshToken = t.RefreshToken
	}
	expiresIn := t.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 60
	}
	cr.AccessExpiresAt = now.Add(time.Duration(expiresIn) * time.Second)
	if name := UsernameFromTokens(t); name != "" {
		cr.Username = name
	}
}

// fresh reports whether the access token is still good at now.
func (cr *Credentials) fresh(now time.Time) bool {
	return cr.AccessToken != "" && now.Add(expirySkew).Before(cr.AccessExpiresAt)
}

// validate rejects a credentials file that was edited into something that
// would send its tokens elsewhere.
func (cr *Credentials) validate() error {
	site, err := NormalizeSite(cr.Site)
	if err != nil || site != cr.Site {
		return fmt.Errorf("its site %q isn't valid", sanitize(cr.Site))
	}
	iss, err := checkTokenURL(cr.Issuer)
	if err != nil {
		return fmt.Errorf("its issuer: %w", err)
	}
	for name, raw := range map[string]string{"token endpoint": cr.TokenEndpoint, "revocation endpoint": cr.RevocationEndpoint} {
		if raw == "" && name == "revocation endpoint" {
			continue
		}
		u, err := checkTokenURL(raw)
		if err != nil {
			return fmt.Errorf("its %s: %w", name, err)
		}
		if !sameOrigin(u, iss) {
			return fmt.Errorf("its %s isn't on the issuer's host", name)
		}
	}
	if !clientIDPattern.MatchString(cr.ClientID) || !tokenPattern.MatchString(cr.AccessToken) ||
		(cr.RefreshToken != "" && !tokenPattern.MatchString(cr.RefreshToken)) {
		return fmt.Errorf("it is incomplete")
	}
	return nil
}

// Store is the credentials file.
type Store struct {
	Path string
}

// DefaultStore is ~/.astrona/credentials.json, next to the CLI's other state.
func DefaultStore() (Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, fmt.Errorf("finding your home directory: %w", err)
	}
	return Store{Path: filepath.Join(home, ".astrona", "credentials.json")}, nil
}

// Load reads the credentials; (nil, nil) when there are none. On Unix it
// refuses a file other users can read, and anything but a regular file.
func (s Store) Load() (*Credentials, error) {
	info, err := os.Lstat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading your Astrona credentials: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file — remove it, then run `astrona login`", s.Path)
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, fmt.Errorf("reading your Astrona credentials: %w", err)
	}
	defer f.Close()
	// Checked on the open file, so it can't be swapped after the Lstat.
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading your Astrona credentials: %w", err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while it was being read — try again", s.Path)
	}
	if runtime.GOOS != "windows" && opened.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users on this machine (mode %04o) — it holds your Astrona sign-in. "+
			"Fix it with `chmod 600 %s` (or `astrona logout` and sign in again)", s.Path, opened.Mode().Perm(), s.Path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("reading your Astrona credentials: %w", err)
	}
	var cr Credentials
	if len(data) > maxResponse || json.Unmarshal(data, &cr) != nil {
		return nil, fmt.Errorf("%s is damaged — run `astrona logout`, then `astrona login`", s.Path)
	}
	if err := cr.validate(); err != nil {
		return nil, fmt.Errorf("%s can't be used (%w) — run `astrona logout`, then `astrona login`", s.Path, err)
	}
	return &cr, nil
}

// Save writes cr atomically (a 0600 temp file in the same directory,
// renamed over the old one), creating the directory 0700 if needed.
func (s Store) Save(cr *Credentials) (err error) {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	data, err := json.MarshalIndent(cr, "", "  ") //nolint:gosec // G117: this 0600 file is where the tokens are meant to be
	if err != nil {
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp") // created 0600
	if err != nil {
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
		return fmt.Errorf("saving your Astrona credentials: %w", err)
	}
	return nil
}

// Delete removes the credentials file; false when there was none.
func (s Store) Delete() (bool, error) {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("removing %s: %w", s.Path, err)
	}
	return true, nil
}

// Active returns credentials for site that hold a fresh access token,
// renewing (and saving) them when the access token has expired. It wraps
// ErrSignedOut when there is no usable sign-in for site: none saved, saved
// for another site, or the refresh token was rejected (the stale file is
// then removed).
func (c *Client) Active(ctx context.Context, s Store, site string) (*Credentials, error) {
	cr, err := s.Load()
	if err != nil {
		return nil, err
	}
	if cr == nil {
		return nil, ErrSignedOut
	}
	if cr.Site != site {
		return nil, fmt.Errorf("you're signed in to %s, not %s: %w", cr.Site, site, ErrSignedOut)
	}
	if cr.fresh(c.Now()) {
		return cr, nil
	}
	return c.Renew(ctx, s, cr)
}

// Renew refreshes cr's access token now and saves the result. A rejected
// refresh token removes the credentials and wraps ErrSignedOut.
func (c *Client) Renew(ctx context.Context, s Store, cr *Credentials) (*Credentials, error) {
	t, err := c.refresh(ctx, cr)
	if err != nil {
		if errors.Is(err, ErrSignedOut) {
			_, _ = s.Delete()
		}
		return nil, err
	}
	next := *cr
	next.apply(t, c.Now())
	if err := s.Save(&next); err != nil {
		return nil, err
	}
	return &next, nil
}
