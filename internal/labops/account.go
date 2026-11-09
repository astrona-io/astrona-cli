package labops

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"astrona/internal/account"
	"astrona/internal/ui"
)

// Account is a signed-in student, for a catalog lab's run/reset.
type Account struct {
	Client *account.Client
	Store  account.Store
	Creds  *account.Credentials
}

// AccountDeps provide optional account tracking (replaced in tests). The
// site is ASTRONA_URL, else the one this computer signed in to, else
// the active build profile's default site.
var AccountDeps = func() (*account.Client, account.Store, string, error) {
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

// AccountForLab returns the active account for optional catalog session
// tracking. Local labs (catalogLab "") and runs without a session
// (untracked: --local-only, headless) get none, without reading
// credentials; so do signed-out catalog labs. Credential storage and
// renewal failures are still reported rather than hidden.
func AccountForLab(ctx context.Context, catalogLab string, untracked bool) (*Account, error) {
	if catalogLab == "" || untracked {
		return nil, nil
	}
	client, store, site, err := AccountDeps()
	if err != nil {
		return nil, err
	}
	creds, err := client.Active(ctx, store, site)
	if errors.Is(err, account.ErrSignedOut) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Account{Client: client, Store: store, Creds: creds}, nil
}

// NoticeOtherSite tells the user on w, right before a request that sends
// them data, when that request does not go to astrona.io: the CLI is open
// source and can sign in to any Astrona — a local one while developing, or
// a self-hosted one. Silent for astrona.io. what is what is being sent
// ("your lab results").
func NoticeOtherSite(w io.Writer, site, what string) {
	if site == "" || account.IsDefaultSite(site) {
		return
	}
	ui.Fwarnf(w, "Not astrona.io — sending %s to %s", what, site)
}

// SameCatalogName compares catalog names, ignoring a leading owner
// ("astrona-io/ATS014/…" is "ATS014/…").
func SameCatalogName(a, b string) bool {
	trim := func(s string) string {
		s = strings.Trim(s, "/")
		if parts := strings.Split(s, "/"); len(parts) == 5 {
			return strings.Join(parts[1:], "/")
		}
		return s
	}
	return a != "" && strings.EqualFold(trim(a), trim(b))
}
