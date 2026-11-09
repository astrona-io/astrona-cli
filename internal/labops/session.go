package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"astrona/internal/account"
	"astrona/internal/labstate"
)

// ValidateOpenURL checks a URL before it is handed to the browser: only an
// absolute http(s) URL with a host and no embedded credentials is opened.
func ValidateOpenURL(raw string) (*url.URL, error) {
	u, err := account.CheckHTTPURL(raw)
	if err != nil {
		return nil, fmt.Errorf("lab page URL: %w", err)
	}
	return u, nil
}

// StartSession asks Astrona for a session for the catalog lab and checks
// the lab page URL it returns — all before the lab is built, so a refusal
// costs nothing. The session token is removed before this URL is retained or
// printed; an authenticated course page reconnects by the attempt ID. It
// says on w that the session started; the notice about another site goes
// to warn. command is the astrona command the errors tell the user to run
// again ("run", "reset").
func StartSession(ctx context.Context, w, warn io.Writer, a *Account, command, lab string, opts ...account.SessionOptions) (*url.URL, *labstate.Session, error) {
	NoticeOtherSite(warn, a.Creds.Site, "this lab session (your account, the lab and its timing)")
	ls, err := a.Client.CreateLabSession(ctx, a.Store, a.Creds, lab, opts...)
	if err != nil {
		return nil, nil, LabSessionError(err, a.Creds.Site, command, lab)
	}
	page, err := ValidateOpenURL(ls.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("%s returned a lab page URL that can't be opened (%w) — nothing was built", a.Creds.Site, err)
	}
	if !account.SameSite(page, a.Creds.Site) {
		return nil, nil, fmt.Errorf("%s returned a lab page on another site (%s) — refusing to open it; nothing was built", a.Creds.Site, page.Host)
	}
	page.RawQuery = ""
	page.ForceQuery = false
	page.Fragment = ""
	who := a.Creds.Username
	if who == "" {
		who = "your account"
	}
	sess := &labstate.Session{Site: a.Creds.Site, ID: ls.ID, Lab: ls.Lab, URL: page.String(), ExpiresAt: ls.ExpiresAt, MaxMinutes: ls.MaxMinutes, Kind: ls.Kind, DeadlineAt: ls.DeadlineAt}
	// A playground — whose watchdog removes the lab at its deadline — only
	// when one was asked for, and never with a deadline outside what was.
	var opt account.SessionOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.Kind != "playground" {
		if sess.IsPlayground() {
			sess.Kind = "lab"
		}
		sess.DeadlineAt = ""
	} else if sess.IsPlayground() {
		sess.DeadlineAt = ClampDeadline(sess.DeadlineAt, time.Now(), opt.TimeLimitMinutes)
	}
	if sess.IsPlayground() {
		fmt.Fprintf(w, "Playground time started for %s on %s — %d minutes until it stops and is removed.\n", who, a.Creds.Site, ls.MaxMinutes)
	} else {
		fmt.Fprintf(w, "Lab session started for %s on %s — the lab page opens once the lab is ready.\n", who, a.Creds.Site)
	}
	if sess.Lab == "" {
		sess.Lab = lab
	}
	return page, sess, nil
}

// LabSessionError turns a failed session request into what to do next.
func LabSessionError(err error, site, command, lab string) error {
	switch {
	case errors.Is(err, account.ErrSignedOut):
		return NotSignedInError(err, command, lab)
	case errors.Is(err, account.ErrTooManySessions):
		return fmt.Errorf("%s won't start another lab session right now (%s). "+
			"Finish or close a lab you have open on %s, or wait a few minutes, then run \"astrona %s %s\" again — nothing was built",
			site, err, site, command, lab)
	case errors.Is(err, account.ErrUnknownLab):
		return fmt.Errorf("%s doesn't recognise the lab %s (%w) — check the name with `astrona labs`; nothing was built", site, lab, err)
	}
	return fmt.Errorf("%w — nothing was built; try again in a moment", err)
}

// NotSignedInError is what a catalog lab says when there is no usable
// sign-in, with why (never signed in, expired, another site).
func NotSignedInError(cause error, command, lab string) error {
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
