package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"astrona/internal/account"

	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show which Astrona account this computer is signed in to",
		Long: "Show the Astrona account and site this computer is signed in to, renewing the sign-in " +
			"if it has expired. Exits 1 when signed out, with how to sign in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, store, site, err := accountDeps()
			if err != nil {
				return err
			}
			return whoami(context.Background(), client, store, site)
		},
	}
}

func whoami(ctx context.Context, client *account.Client, store account.Store, site string) error {
	creds, err := client.Active(ctx, store, site)
	if errors.Is(err, account.ErrSignedOut) {
		if err == account.ErrSignedOut { //nolint:errorlint // the bare sentinel: never signed in
			return fmt.Errorf("you're not signed in to Astrona (%s) — sign in with: astrona login", site)
		}
		reason := strings.TrimSuffix(err.Error(), ": "+account.ErrSignedOut.Error())
		return fmt.Errorf("%s — sign in with: astrona login", reason)
	}
	if err != nil {
		return err
	}
	if creds.Username != "" {
		fmt.Printf("Signed in as %s on %s\n", creds.Username, creds.Site)
	} else {
		fmt.Printf("Signed in on %s\n", creds.Site)
	}
	return nil
}
