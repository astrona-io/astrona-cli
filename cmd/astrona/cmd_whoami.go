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
	var output string
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show which Astrona account this computer is signed in to",
		Long: "Show the Astrona account and site this computer is signed in to, renewing the sign-in " +
			"if it has expired. Exits 1 when signed out, with how to sign in.\n\n" +
			"With -o json, stdout is one document {signedIn, username, site} — also when signed " +
			"out (signedIn: false), which still exits 1.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			client, store, site, err := accountDeps()
			if err != nil {
				return err
			}
			return whoami(context.Background(), client, store, site, output == "json")
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

// whoamiJSON is `astrona whoami -o json`.
type whoamiJSON struct {
	SignedIn bool   `json:"signedIn"`
	Username string `json:"username,omitempty"`
	Site     string `json:"site"`
}

func whoami(ctx context.Context, client *account.Client, store account.Store, site string, asJSON bool) error {
	creds, err := client.Active(ctx, store, site)
	if errors.Is(err, account.ErrSignedOut) {
		if asJSON {
			if perr := printJSON(whoamiJSON{SignedIn: false, Site: site}); perr != nil {
				return perr
			}
		}
		if err == account.ErrSignedOut { //nolint:errorlint // the bare sentinel: never signed in
			return fmt.Errorf("you're not signed in to Astrona (%s) — sign in with: astrona login", site)
		}
		reason := strings.TrimSuffix(err.Error(), ": "+account.ErrSignedOut.Error())
		return fmt.Errorf("%s — sign in with: astrona login", reason)
	}
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(whoamiJSON{SignedIn: true, Username: creds.Username, Site: creds.Site})
	}
	if creds.Username != "" {
		fmt.Printf("Signed in as %s on %s\n", creds.Username, creds.Site)
	} else {
		fmt.Printf("Signed in on %s\n", creds.Site)
	}
	return nil
}
