package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"astrona/internal/account"

	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in to your Astrona account (catalog labs need it)",
		Long: "Sign this computer in to your Astrona account. Catalog labs (`astrona run ATS014/…`) are tied " +
			"to your account: `astrona run` starts a lab session for you and opens the lab page, which " +
			"tracks your time. Labs from your own files or repositories (-c / --git) don't need it.\n\n" +
			"Sign-in happens in your browser (OAuth device flow): astrona shows a short code and opens " +
			"the sign-in page; confirm the code there and the terminal finishes by itself. No password is " +
			"ever typed into the terminal. On a machine without a browser, open the link on any device.\n\n" +
			"The sign-in is saved in ~/.astrona/credentials.json (readable only by you) and renewed " +
			"automatically. `astrona whoami` shows who is signed in, `astrona logout` signs out.\n\n" +
			"The site is " + account.DefaultSite + " unless " + account.SiteEnv + " points elsewhere " +
			"(for example http://localhost:3000 for local development).",
		Example: `  astrona login
  ASTRONA_URL=http://localhost:3000 astrona login`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, store, site, err := accountDeps()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			return login(ctx, client, store, site)
		},
	}
}

// login runs the device flow against site and saves the credentials.
func login(ctx context.Context, client *account.Client, store account.Store, site string) error {
	// An earlier sign-in is replaced; its refresh token is revoked once the
	// new one is saved. One that can't be read is simply overwritten.
	previous, _ := store.Load()

	fmt.Printf("Signing in to %s…\n", site)
	sc, err := client.FetchSiteConfig(ctx, site)
	if err != nil {
		return err
	}
	p, err := client.Discover(ctx, sc.Issuer)
	if err != nil {
		return err
	}
	dc, err := client.StartDeviceFlow(ctx, p, sc.ClientID)
	if err != nil {
		return err
	}

	fmt.Printf("\nOpening your browser… If it doesn't open, go to %s and enter code %s\n", dc.VerificationURI, dc.UserCode)
	target := dc.VerificationURIComplete
	if target == "" {
		target = dc.VerificationURI
	}
	if !browserOpener(target) {
		fmt.Println("(No browser here — open that link on any device; this terminal waits for you.)")
	}
	fmt.Println("Waiting for you to confirm in the browser… (Ctrl+C cancels)")

	tokens, err := client.PollToken(ctx, p, sc.ClientID, dc)
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("sign-in cancelled — you're not signed in")
	case errors.Is(err, account.ErrAccessDenied):
		return fmt.Errorf("the sign-in was declined in the browser — run \"astrona login\" to try again")
	case errors.Is(err, account.ErrDeviceCodeExpired):
		return fmt.Errorf("the code expired before it was confirmed (it's valid for %s) — run \"astrona login\" for a new one",
			time.Duration(dc.ExpiresIn)*time.Second)
	case err != nil:
		return err
	}

	creds := account.NewCredentials(site, sc, p, tokens, client.Now())
	if err := store.Save(creds); err != nil {
		return err
	}
	if previous != nil && previous.RefreshToken != "" && previous.RefreshToken != creds.RefreshToken {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.Revoke(rctx, previous.RevocationEndpoint, previous.ClientID, previous.RefreshToken) // best effort
		cancel()
	}

	if creds.Username != "" {
		fmt.Printf("\nSigned in as %s on %s\n", creds.Username, site)
	} else {
		fmt.Printf("\nSigned in on %s\n", site)
	}
	fmt.Println("Next: astrona labs — then astrona run <lab>")
	return nil
}
