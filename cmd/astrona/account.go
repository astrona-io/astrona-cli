package main

import (
	"context"

	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/workspace"
)

// accountForLab returns the active account for optional catalog session tracking.
// Local labs and signed-out catalog labs run without a session. Credential
// storage and renewal failures are still reported rather than hidden.
func accountForLab(ctx context.Context, flags *rootFlags) (*labops.Account, error) {
	return labops.AccountForLab(ctx, flags.catalogLab, flags.localOnly || flags.headless)
}

// runAccountStatus is what the browser workspace says about the account
// this run is saved to, from what `astrona run` did: sess is the lab
// session it started (nil: none) under acct. A catalog lab without a
// session and without --local-only ran signed out; its site is the one
// `astrona login` would sign in to, for the page's sign-up link. Never a
// token, device or key id.
func runAccountStatus(flags *rootFlags, acct *labops.Account, sess *labstate.Session) *workspace.AccountStatus {
	switch {
	case flags.catalogLab == "" || flags.headless:
		return &workspace.AccountStatus{State: workspace.AccountUntracked}
	case flags.localOnly:
		return &workspace.AccountStatus{State: workspace.AccountLocalOnly}
	case sess != nil && acct != nil && acct.Creds != nil:
		return &workspace.AccountStatus{
			State:    workspace.AccountSignedIn,
			Username: labops.SanitizeLine(acct.Creds.Username),
			Site:     acct.Creds.Site,
		}
	}
	st := &workspace.AccountStatus{State: workspace.AccountSignedOut}
	if _, _, site, err := labops.AccountDeps(); err == nil {
		st.Site = site
	}
	return st
}
