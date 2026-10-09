package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"astrona/internal/account"
	"astrona/internal/labstate"
)

// labAccount is a signed-in student, for a catalog lab's run/reset.
type labAccount struct {
	client *account.Client
	store  account.Store
	creds  *account.Credentials
}

// accountDeps are what the sign-in gate needs (replaced in tests). The
// site is ASTRONA_URL, else the one this computer signed in to, else
// account.DefaultSite.
var accountDeps = func() (*account.Client, account.Store, string, error) {
	store, err := account.DefaultStore()
	if err != nil {
		return nil, account.Store{}, "", err
	}
	saved := ""
	if cr, err := store.Load(); err == nil && cr != nil { // an unreadable file is reported by whoever uses it
		saved = cr.Site
	}
	site, err := account.ResolveSite(os.Getenv, saved)
	if err != nil {
		return nil, account.Store{}, "", err
	}
	return account.NewClient(), store, site, nil
}

// requireSignIn is the gate in front of a catalog lab: nil when the lab
// isn't from the catalog (-c / --git labs never need an account), else the
// signed-in account — or, before anything is fetched or built, an error
// saying how to sign in.
func requireSignIn(ctx context.Context, flags *rootFlags, command string) (*labAccount, error) {
	if flags.catalogLab == "" {
		return nil, nil
	}
	client, store, site, err := accountDeps()
	if err != nil {
		return nil, err
	}
	creds, err := client.Active(ctx, store, site)
	if errors.Is(err, account.ErrSignedOut) {
		return nil, notSignedInError(err, command, flags.catalogLab)
	}
	if err != nil {
		return nil, err
	}
	return &labAccount{client: client, store: store, creds: creds}, nil
}

// notSignedInError is what a catalog lab says when there is no usable
// sign-in, with why (never signed in, expired, another site).
func notSignedInError(cause error, command, lab string) error {
	reason := "You're not signed in to Astrona."
	if cause != account.ErrSignedOut { //nolint:errorlint // only the bare sentinel has no reason to show
		if r := strings.TrimSuffix(cause.Error(), ": "+account.ErrSignedOut.Error()); r != cause.Error() {
			reason = capitalize(r) + "."
		}
	}
	return fmt.Errorf("%s Catalog labs are tied to your account so the lab page can track your time — "+
		"run \"astrona login\" first, then \"astrona %s %s\" again.\n"+
		"(Labs from your own files or repositories, -c / --git, run without signing in.)", reason, command, lab)
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// startSession asks Astrona for a session for the catalog lab and checks
// the lab page URL it returns — all before the lab is built, so a refusal
// costs nothing. The URL must be on the signed-in site. The session comes
// back as it is remembered for the lab (labstate) — without its token.
func (a *labAccount) startSession(ctx context.Context, command, lab string, opts ...account.SessionOptions) (*url.URL, *labstate.Session, error) {
	noticeOtherSite(a.creds.Site, "this lab session (your account, the lab and its timing)")
	ls, err := a.client.CreateLabSession(ctx, a.store, a.creds, lab, opts...)
	if err != nil {
		return nil, nil, labSessionError(err, a.creds.Site, command, lab)
	}
	page, err := validateOpenURL(ls.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("%s returned a lab page URL that can't be opened (%w) — nothing was built", a.creds.Site, err)
	}
	if !account.SameSite(page, a.creds.Site) {
		return nil, nil, fmt.Errorf("%s returned a lab page on another site (%s) — refusing to open it; nothing was built", a.creds.Site, page.Host)
	}
	who := a.creds.Username
	if who == "" {
		who = "your account"
	}
	sess := &labstate.Session{Site: a.creds.Site, ID: ls.ID, Lab: ls.Lab, URL: page.String(), ExpiresAt: ls.ExpiresAt, MaxMinutes: ls.MaxMinutes, Kind: ls.Kind, DeadlineAt: ls.DeadlineAt}
	if sess.IsPlayground() {
		fmt.Printf("Playground time started for %s on %s — %d minutes until it stops and is removed.\n", who, a.creds.Site, ls.MaxMinutes)
	} else {
		fmt.Printf("Lab session started for %s on %s — the lab page opens once the lab is ready.\n", who, a.creds.Site)
	}
	if sess.Lab == "" {
		sess.Lab = lab
	}
	return page, sess, nil
}

// labSessionError turns a failed session request into what to do next.
func labSessionError(err error, site, command, lab string) error {
	switch {
	case errors.Is(err, account.ErrSignedOut):
		return notSignedInError(err, command, lab)
	case errors.Is(err, account.ErrTooManySessions):
		return fmt.Errorf("%s won't start another lab session right now (%s). "+
			"Finish or close a lab you have open on %s, or wait a few minutes, then run \"astrona %s %s\" again — nothing was built",
			site, err, site, command, lab)
	case errors.Is(err, account.ErrUnknownLab):
		return fmt.Errorf("%s doesn't recognise the lab %s (%w) — check the name with `astrona labs`; nothing was built", site, lab, err)
	}
	return fmt.Errorf("%w — nothing was built; try again in a moment", err)
}
