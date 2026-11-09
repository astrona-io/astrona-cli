package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"astrona/internal/account"
	"astrona/internal/apphome"
	"astrona/internal/labops"

	"github.com/spf13/cobra"
)

// revokeTimeout bounds the call that revokes a sign-in on the site: signing out
// locally must not hang on a server that's down.
const revokeTimeout = 5 * time.Second

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out of Astrona on this computer",
		Long: "Sign out: revoke this computer's sign-in on the Astrona website (best effort, a few " +
			"seconds at most) and delete ~/" + apphome.Name() + "/credentials.json. Catalog labs then need " +
			"`astrona login` again; labs that are already running keep running, but their usage reporters stop: " +
			"no more CPU and memory samples are sent for them. Samples not sent yet wait on this computer until " +
			"you sign in again (or are dropped when `astrona settings set usage-samples no` is set). " +
			"A playground's watchdog keeps running so the playground is still removed at its time limit.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, store, _, err := labops.AccountDeps()
			if err != nil {
				return err
			}
			return logout(client, store)
		},
	}
}

func logout(client *account.Client, store account.Store) error {
	creds, loadErr := store.Load()
	if loadErr == nil && creds == nil {
		fmt.Println("You're not signed in — nothing to do.")
		return nil
	}

	revoked := "nothing to revoke on the server"
	if creds != nil {
		ctx, cancel := context.WithTimeout(context.Background(), revokeTimeout)
		labops.NoticeOtherSite(os.Stderr, creds.Site, "the sign-out")
		err := client.Revoke(ctx, creds.Site, creds.RefreshToken)
		cancel()
		if err != nil {
			revoked = fmt.Sprintf("couldn't revoke it on the server (%s) — it stays valid there until it expires", err)
		} else {
			revoked = "revoked on the server"
		}
	}
	if _, err := store.Delete(); err != nil {
		return err
	}
	// The reporters post with this sign-in: none keeps sending after it.
	stopped := labops.StopUsageReporters()

	switch {
	case creds == nil && errors.Is(loadErr, account.ErrSignedOut):
		fmt.Printf("Removed the sign-in an older astrona saved in %s. You're signed out.\n", store.Path)
	case creds == nil:
		fmt.Printf("Removed %s (it couldn't be used: %s). You're signed out.\n", store.Path, loadErr)
	case creds.Username != "":
		fmt.Printf("Signed out %s from %s (%s).\n", creds.Username, creds.Site, revoked)
	default:
		fmt.Printf("Signed out from %s (%s).\n", creds.Site, revoked)
	}
	if len(stopped) > 0 {
		fmt.Printf("Stopped sending CPU and memory use for %s.\n", strings.Join(stopped, ", "))
	}
	return nil
}
