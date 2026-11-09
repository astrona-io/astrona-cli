package main

import (
	"context"
	"net/url"
	"os"

	"astrona/internal/account"
	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// labAccount is a signed-in student, for a catalog lab's run/reset.
type labAccount = labops.Account

// accountDeps are the account, credentials store and site the CLI uses
// (labops.AccountDeps, replaced in tests).
func accountDeps() (*account.Client, account.Store, string, error) { return labops.AccountDeps() }

// accountForLab returns the active account for optional catalog session tracking.
// Local labs and signed-out catalog labs run without a session. Credential
// storage and renewal failures are still reported rather than hidden.
func accountForLab(ctx context.Context, flags *rootFlags) (*labAccount, error) {
	return labops.AccountForLab(ctx, flags.catalogLab, flags.localOnly || flags.headless)
}

// notSignedInError is what a catalog lab says when there is no usable
// sign-in (labops.NotSignedInError).
func notSignedInError(cause error, command, lab string) error {
	return labops.NotSignedInError(cause, command, lab)
}

// startSession asks Astrona for a session for the catalog lab before the
// lab is built (labops.StartSession); it says so on stdout.
func startSession(ctx context.Context, a *labAccount, command, lab string, opts ...account.SessionOptions) (*url.URL, *labstate.Session, error) {
	return labops.StartSession(ctx, os.Stdout, os.Stderr, a, command, lab, opts...)
}
