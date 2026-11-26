package labops

import (
	"context"
	"io"
	"time"

	"astrona/internal/account"
	"astrona/internal/guestrun"
	"astrona/internal/transport"
	"astrona/internal/ui"
)

// signInRevokeTimeout bounds the call that revokes the sign-in a new one
// replaced.
const signInRevokeTimeout = 5 * time.Second

// SignIn is one sign-in through the site (`astrona login`, or the browser
// workspace's "Sign in" before Begin): BeginSignIn asks the site for a
// code, the student authorizes this computer on the site's own page, and
// Finish waits for that and saves the sign-in. The device code it polls
// with never leaves it; only the user code and the authorize page are
// shown to the student.
type SignIn struct {
	client  *account.Client
	store   account.Store
	site    string
	sc      account.SiteConfig
	dc      account.DeviceCode
	device  account.Device
	expires time.Time
}

// BeginSignIn starts a sign-in on site for device (its name and OS on the
// site's device list): it fetches the site's CLI sign-in configuration,
// makes a new device key and asks for a code. A site without CLI sign-in
// answers with an error account.IsNotFound reports. The notice for a site
// other than astrona.io goes to warn.
func BeginSignIn(ctx context.Context, warn io.Writer, client *account.Client, store account.Store, site string, device account.Device) (*SignIn, error) {
	// An earlier sign-in is replaced; its refresh token is revoked once the
	// new one is saved. One that can't be read is simply overwritten.
	previous, _ := store.Load()
	sc, err := client.FetchSiteConfig(ctx, site)
	if err != nil {
		return nil, err
	}
	NoticeOtherSite(warn, site, "your sign-in (this device's name)")
	if previous != nil && previous.Site == site {
		// Signing this computer in again replaces its sign-in rather than
		// counting as another computer against the account's limit.
		device.PreviousRefreshToken = previous.RefreshToken
	}
	// A new device key for every sign-in; the previous one is dropped once
	// this sign-in is saved. Without one (no random source) the sign-in
	// still works, as it did before device keys.
	if key, err := transport.Generate(); err == nil {
		device = device.WithKey(key)
	}
	dc, err := client.StartDeviceFlow(ctx, site, sc, device)
	if err != nil {
		return nil, err
	}
	expires := client.Now().Add(30 * time.Minute)
	if dc.ExpiresIn > 0 && time.Duration(dc.ExpiresIn)*time.Second < 30*time.Minute {
		expires = client.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	}
	return &SignIn{client: client, store: store, site: site, sc: sc, dc: dc, device: device, expires: expires}, nil
}

// Site is the site being signed in to.
func (s *SignIn) Site() string { return s.site }

// UserCode is the code the student checks against the one on the site's
// page. It grants nothing by itself.
func (s *SignIn) UserCode() string { return s.dc.UserCode }

// VerificationURL is the site's page where the student authorizes this
// computer (on the site, checked by account.StartDeviceFlow).
func (s *SignIn) VerificationURL() string { return s.dc.VerificationURIComplete }

// ExpiresAt is when the code stops working.
func (s *SignIn) ExpiresAt() time.Time { return s.expires }

// Finish waits until the student authorized (or declined) this computer,
// then saves the sign-in, replaces the anonymous install id and revokes
// the sign-in it replaced. Its errors are PollToken's: account.ErrAccessDenied,
// ErrDeviceCodeExpired, ErrDeviceLimit, or ctx's. Warnings go to warn.
func (s *SignIn) Finish(ctx context.Context, warn io.Writer) (*account.Credentials, error) {
	tokens, err := s.client.PollToken(ctx, s.sc, s.dc)
	if err != nil {
		return nil, err
	}
	creds := account.NewCredentials(s.site, s.device, tokens, s.client.Now())
	release, err := s.store.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	// A reporter may have renewed the old sign-in during authorization.
	// Revoke the credentials being replaced, using their current token.
	previous, _ := s.store.Load()
	if err := s.store.SaveSignIn(creds); err != nil {
		return nil, err
	}
	// Guest runs after this sign-in never share the anonymous install id
	// of the ones before it.
	if err := guestrun.RotateInstallID(); err != nil {
		ui.Fwarnf(warn, "could not replace the anonymous install id: %s", err)
	}
	if previous != nil && previous.RefreshToken != creds.RefreshToken {
		rctx, cancel := context.WithTimeout(context.Background(), signInRevokeTimeout)
		_ = s.client.Revoke(rctx, previous.Site, previous.RefreshToken) // best effort
		cancel()
	}
	return creds, nil
}
