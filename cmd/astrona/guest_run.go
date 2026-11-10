package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"astrona/internal/account"
	"astrona/internal/labops"
	"astrona/internal/settings"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type guestRunChoice struct{ accepted, login, never bool }

// askGuestRun asks a signed-out user in a terminal whether to log in before
// the lab starts: yes logs in, no continues as a guest, never continues as a
// guest and saves login-reminder=no. The question goes to out (stderr).
func askGuestRun(in io.Reader, out io.Writer, local bool) (guestRunChoice, error) {
	text := "You're not logged in. Log in to track your lab progress and results on Astrona."
	if local {
		text = "You're not logged in. Log in to track progress on catalog labs. This local lab will stay local."
	}
	fmt.Fprintln(out, text)
	for {
		fmt.Fprint(out, "Log in now? (yes = log in, no = continue as guest, never = continue as guest and stop asking): ")
		answer, err := readAnswerLine(in)
		if err != nil {
			return guestRunChoice{}, fmt.Errorf("login question interrupted; no lab was started: %w", err)
		}
		switch answer {
		case "yes", "y":
			return guestRunChoice{accepted: true, login: true}, nil
		case "no", "n":
			return guestRunChoice{accepted: true}, nil
		case "never":
			return guestRunChoice{accepted: true, never: true}, nil
		default:
			fmt.Fprintln(out, "Please enter yes, no or never.")
		}
	}
}

func guestReminderEnabled(flags *rootFlags) (bool, error) {
	path, err := settings.Path(flags.settingsFile)
	if err != nil {
		return false, err
	}
	saved, err := settings.Load(path)
	if err != nil {
		return false, err
	}
	value, err := saved.Value("login-reminder")
	return string(value) == "true", err
}

func saveGuestChoice(flags *rootFlags, choice guestRunChoice) error {
	if !choice.accepted || !choice.never {
		return nil
	}
	path, err := settings.Path(flags.settingsFile)
	if err != nil {
		return err
	}
	value := "false"
	return settings.Change(path, "login-reminder", &value)
}

// Explicit local-only restarts never access credentials or the account API.
func confirmGuestLabRun(cmd *cobra.Command, flags *rootFlags, acct *labops.Account, withoutLogin bool) (*labops.Account, bool, error) {
	if flags.localOnly || flags.headless || acct != nil {
		return acct, true, nil
	}
	tty := isatty.IsTerminal(os.Stdin.Fd())
	if withoutLogin || !tty {
		approved, err := approveUntrackedLab(cmd.InOrStdin(), cmd.ErrOrStderr(), tty, withoutLogin, flags.catalogLab, acct)
		return acct, approved, err
	}
	enabled, err := guestReminderEnabled(flags)
	if err != nil || !enabled {
		return acct, err == nil, err
	}
	if flags.catalogLab == "" {
		_, store, site, err := labops.AccountDeps()
		if err != nil {
			return nil, false, err
		}
		creds, err := store.Load()
		if err != nil {
			return nil, false, err
		}
		if creds != nil && creds.Site == site {
			return acct, true, nil
		}
	}
	choice, err := askGuestRun(cmd.InOrStdin(), promptOut, flags.catalogLab == "")
	if err != nil {
		return nil, false, err
	}
	if err := saveGuestChoice(flags, choice); err != nil {
		return nil, false, err
	}
	if !choice.accepted || !choice.login {
		return nil, choice.accepted, nil
	}
	client, store, site, err := labops.AccountDeps()
	if err != nil {
		return nil, false, err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	return loginForRun(ctx, flags, client, store, site)
}

func loginForRun(ctx context.Context, flags *rootFlags, client *account.Client, store account.Store, site string) (*labops.Account, bool, error) {
	if err := login(ctx, client, store, site, thisDevice()); err != nil {
		return nil, false, err
	}
	acct, err := accountForLab(ctx, flags)
	return acct, err == nil, err
}
