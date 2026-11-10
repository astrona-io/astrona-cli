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

	"astrona/internal/apphome"
	"astrona/internal/filelock"
)

// expirySkew renews an access token this long before it expires, so it
// doesn't run out between the check and the request.
const expirySkew = 30 * time.Second

// Credentials is what `astrona login` saves. AccessToken and RefreshToken
// are secrets issued by Site: never print or log them, never send them
// anywhere but Site.
type Credentials struct {
	Site            string    `json:"site"`
	AccessToken     string    `json:"access_token"`
	RefreshToken    string    `json:"refresh_token"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
	Username        string    `json:"username,omitempty"`
	DeviceID        string    `json:"device_id,omitempty"`
	DeviceName      string    `json:"device_name,omitempty"`
}

// NewCredentials builds the credentials for tokens site issued to device.
func NewCredentials(site string, d Device, t Tokens, now time.Time) *Credentials {
	cr := &Credentials{Site: site, DeviceName: d.Name}
	cr.apply(t, now)
	return cr
}

// apply stores tokens (the refresh token rotates on every renewal).
func (cr *Credentials) apply(t Tokens, now time.Time) {
	cr.AccessToken = t.AccessToken
	cr.RefreshToken = t.RefreshToken
	expiresIn := t.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 60
	}
	cr.AccessExpiresAt = now.Add(time.Duration(expiresIn) * time.Second)
	if t.Username != "" {
		cr.Username = t.Username
	}
	if t.DeviceID != "" {
		cr.DeviceID = t.DeviceID
	}
}

// fresh reports whether the access token is still good at now.
func (cr *Credentials) fresh(now time.Time) bool {
	return cr.AccessToken != "" && now.Add(expirySkew).Before(cr.AccessExpiresAt)
}

// sameSignIn distinguishes token rotation from a replacement login. Older
// files without a device ID can only be matched by their unchanged token.
func (cr *Credentials) sameSignIn(other *Credentials) bool {
	if cr == nil || other == nil || cr.Site != other.Site {
		return false
	}
	if cr.DeviceID != "" && other.DeviceID != "" {
		return cr.DeviceID == other.DeviceID
	}
	return cr.RefreshToken != "" && cr.RefreshToken == other.RefreshToken
}

// validate rejects a credentials file that was edited into something that
// would send its tokens elsewhere, and makes its display fields safe to
// print.
func (cr *Credentials) validate() error {
	site, err := NormalizeSite(cr.Site)
	if err != nil || site != cr.Site {
		return fmt.Errorf("its site %q isn't valid", sanitize(cr.Site))
	}
	if !tokenPattern.MatchString(cr.AccessToken) || !tokenPattern.MatchString(cr.RefreshToken) {
		return fmt.Errorf("it is incomplete")
	}
	cr.Username = sanitize(cr.Username)
	cr.DeviceID = sanitize(cr.DeviceID)
	cr.DeviceName = sanitize(cr.DeviceName)
	return nil
}

// errOldFormat is a credentials file written by an astrona that signed in
// with Keycloak: its tokens aren't the site's, so it is a signed-out state.
var errOldFormat = fmt.Errorf("your saved sign-in is from an older version of astrona and no longer works: %w", ErrSignedOut)

// isOldFormat reports whether data is a pre-website-sign-in credentials file.
func isOldFormat(data []byte) bool {
	var old struct {
		Issuer   *string `json:"issuer"`
		ClientID *string `json:"client_id"`
	}
	return json.Unmarshal(data, &old) == nil && (old.Issuer != nil || old.ClientID != nil)
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
	return Store{Path: filepath.Join(home, apphome.Name(), "credentials.json")}, nil
}

// Load reads the credentials; (nil, nil) when there are none. On Unix it
// refuses a file other users can read, and anything but a regular file. A
// file from an older astrona (Keycloak sign-in) wraps ErrSignedOut.
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
	if len(data) <= maxResponse && isOldFormat(data) {
		return nil, errOldFormat
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
// Callers replacing a sign-in must hold Lock through their read and write.
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
// Callers must hold Lock through their read and deletion.
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

// DeleteIfCurrent removes the sign-in expected belongs to, including any
// renewed tokens for the same device. A replacement login is preserved.
func (s Store) DeleteIfCurrent(ctx context.Context, expected *Credentials) (bool, error) {
	release, err := s.Lock(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	saved, err := s.Load()
	if err != nil || !expected.sameSignIn(saved) {
		return false, err
	}
	return s.Delete()
}

// Active returns credentials for site that hold a fresh access token,
// renewing (and saving) them when the access token has expired. It wraps
// ErrSignedOut when there is no usable sign-in for site: none saved, saved
// for another site or by an older astrona, or the refresh token was
// rejected (the stale file is then removed).
func (c *Client) Active(ctx context.Context, s Store, site string) (*Credentials, error) {
	cr, err := s.Load()
	if errors.Is(err, errOldFormat) {
		release, lockErr := s.Lock(ctx)
		if lockErr != nil {
			return nil, lockErr
		}
		// Login may have replaced the old format while we waited.
		cr, err = s.Load()
		if errors.Is(err, errOldFormat) {
			_, _ = s.Delete()
		}
		release()
	}
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

// renewLockWait bounds how long Renew waits for another astrona's renewal
// (two requests of at most requestTimeout each) before giving up.
const renewLockWait = 3 * requestTimeout

// lockPath is the lock file that serializes sign-in changes across processes.
func (s Store) lockPath() string { return s.Path + ".lock" }

// Lock serializes login, logout and token renewal. The returned function
// releases it. Browser authorization must finish before taking this lock.
func (s Store) Lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return nil, fmt.Errorf("could not lock your sign-in: %w", err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, renewLockWait)
	defer cancel()
	release, err := filelock.Wait(lockCtx, s.lockPath())
	if err != nil {
		return nil, fmt.Errorf("could not lock your sign-in (another astrona may be updating it — try again): %w", err)
	}
	return release, nil
}

// Renew refreshes cr's access token now and saves the result before
// returning it: the refresh token rotates, so the new one must be on disk
// before anything else happens. Renewals are serialized across processes
// by a lock next to the credentials file, and the saved sign-in is read
// again once the lock is held: when another astrona renewed it meanwhile,
// that sign-in is used (or renewed in turn) instead of spending cr's
// refresh token, which the site no longer accepts. A rejected refresh
// token removes the credentials and wraps ErrSignedOut.
func (c *Client) Renew(ctx context.Context, s Store, cr *Credentials) (*Credentials, error) {
	release, err := s.Lock(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not renew your sign-in: %w", err)
	}
	defer release()

	saved, err := s.Load()
	switch {
	case errors.Is(err, errOldFormat):
		_, _ = s.Delete()
		return nil, err
	case err != nil:
		return nil, err
	case saved == nil:
		return nil, ErrSignedOut // signed out (or rejected) by another astrona meanwhile
	case saved.Site != cr.Site:
		return nil, fmt.Errorf("you're signed in to %s, not %s: %w", saved.Site, cr.Site, ErrSignedOut)
	case !cr.sameSignIn(saved):
		return nil, fmt.Errorf("your saved sign-in changed; retry the command: %w", ErrSignedOut)
	case saved.RefreshToken != cr.RefreshToken:
		// Another astrona renewed it while this one waited.
		if saved.fresh(c.Now()) {
			return saved, nil
		}
		cr = saved
	}

	t, err := c.refresh(ctx, cr)
	if err != nil {
		if errors.Is(err, ErrSignedOut) {
			// An astrona that renews without the lock (an older version) may
			// still have rotated it: keep its sign-in rather than delete it.
			if now, lerr := s.Load(); lerr == nil && now != nil && !cr.sameSignIn(now) {
				return nil, fmt.Errorf("your saved sign-in changed; retry the command: %w", ErrSignedOut)
			} else if lerr == nil && now != nil && now.RefreshToken != cr.RefreshToken {
				if now.fresh(c.Now()) {
					return now, nil
				}
				return nil, fmt.Errorf("your sign-in was renewed by another astrona at the same time — try again")
			}
			_, _ = s.Delete()
		}
		return nil, err
	}
	next := *cr
	next.apply(t, c.Now())
	if err := s.Save(&next); err != nil {
		return nil, fmt.Errorf("your sign-in was renewed but couldn't be saved, so it is lost — run `astrona login` again: %w", err)
	}
	return &next, nil
}
