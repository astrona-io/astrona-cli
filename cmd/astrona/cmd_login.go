package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"

	"astrona/internal/account"

	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var siteFlag string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to your Astrona account (catalog labs need it)",
		Long: "Sign this computer in to your Astrona account. Catalog labs (`astrona run astrona.io/ATS014/…`) are tied " +
			"to your account: `astrona run` starts a lab session for you and opens the lab page, which " +
			"tracks your time. Labs from your own files or repositories (-c / --git) don't need it.\n\n" +
			"Sign-in happens on the Astrona website: astrona opens a page on astrona.io in your browser. " +
			"If you're already signed in there, check that the code matches the one in the terminal and " +
			"click Authorize; otherwise sign in on astrona.io first and you're brought back to that page. " +
			"The terminal waits and finishes by itself. No password is ever typed into the terminal. On a " +
			"machine without a browser, open the printed link on any device.\n\n" +
			"The sign-in is saved in ~/.astrona/credentials.json (readable only by you) and renewed " +
			"automatically. `astrona whoami` shows who is signed in, `astrona logout` signs out.\n\n" +
			"The site is " + account.DefaultSite + " unless --site (or " + account.SiteEnv + ") names another, " +
			"for example http://localhost:3000 for local development. The site is remembered with the " +
			"sign-in, so whoami, logout and catalog labs use it without repeating it; " + account.SiteEnv +
			" still overrides it per command. Plain http:// is only accepted for localhost.",
		Example: `  astrona login
  astrona login --site http://localhost:3000     # local development; remembered
  ASTRONA_URL=https://staging.astrona.io astrona whoami`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, store, site, err := accountDeps()
			if err != nil {
				return err
			}
			if siteFlag != "" {
				if site, err = account.NormalizeSite(siteFlag); err != nil {
					return fmt.Errorf("--site %q: %w", siteFlag, err)
				}
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			return login(ctx, client, store, site, thisDevice())
		},
	}
	cmd.Flags().StringVar(&siteFlag, "site", "", "Sign in to this Astrona site (e.g. http://localhost:3000) instead of "+account.DefaultSite+"; it's remembered for later commands")
	return cmd
}

// thisDevice is how this computer is named on the site's device list.
func thisDevice() account.Device {
	host, _ := os.Hostname()
	return account.Device{Name: account.DeviceName(host), OS: deviceOS(runtime.GOOS, os.Getenv)}
}

// deviceOS is the OS reported to the site: GOOS, or "linux-wsl" under WSL.
func deviceOS(goos string, getenv func(string) string) string {
	if goos == "linux" && isWSL(getenv) {
		return goos + "-wsl"
	}
	return goos
}

// login signs in through the site: the student authorizes this computer on
// the site's own page, and the site issues the tokens, which are saved.
func login(ctx context.Context, client *account.Client, store account.Store, site string, device account.Device) error {
	// An earlier sign-in is replaced; its refresh token is revoked once the
	// new one is saved. One that can't be read is simply overwritten.
	previous, _ := store.Load()

	fmt.Printf("Signing in to %s…\n", site)
	sc, err := client.FetchSiteConfig(ctx, site)
	if account.IsNotFound(err) {
		return fmt.Errorf("%w\n%s doesn't offer astrona sign-in (no %s/api/cli/config) — for another site, e.g. a local one: astrona login --site http://localhost:3000", err, site, site)
	}
	if err != nil {
		return err
	}
	noticeOtherSite(site, "your sign-in (this device's name)")
	dc, err := client.StartDeviceFlow(ctx, site, sc, device)
	if err != nil {
		return err
	}

	fmt.Printf("\nOpening your browser to sign in… If it doesn't open, go to %s\n", dc.VerificationURIComplete)
	fmt.Printf("Confirm the code %s matches the one in your browser.\n", dc.UserCode)
	if !browserOpener(dc.VerificationURIComplete) {
		fmt.Println("(No browser here — open that link on any device; this terminal waits for you.)")
	}
	fmt.Println("Waiting for you to authorize this computer… (Ctrl+C cancels)")

	tokens, err := client.PollToken(ctx, sc, dc)
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("sign-in cancelled — you're not signed in")
	case errors.Is(err, account.ErrAccessDenied):
		return fmt.Errorf("Sign-in was declined in the browser.") //nolint:staticcheck // ST1005: shown to the student as is
	case errors.Is(err, account.ErrDeviceCodeExpired):
		return fmt.Errorf("The code expired — run astrona login again.") //nolint:staticcheck // ST1005: shown to the student as is
	case err != nil:
		return err
	}

	creds := account.NewCredentials(site, device, tokens, client.Now())
	if err := store.Save(creds); err != nil {
		return err
	}
	if previous != nil && previous.RefreshToken != creds.RefreshToken {
		rctx, cancel := context.WithTimeout(context.Background(), revokeTimeout)
		_ = client.Revoke(rctx, previous.Site, previous.RefreshToken) // best effort
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
