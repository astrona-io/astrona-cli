package main

import (
	"fmt"
	"io"
)

// approveUntrackedLab asks before starting a catalog lab without account tracking.
// Automation must explicitly opt in rather than silently losing progress tracking.
func approveUntrackedLab(in io.Reader, out io.Writer, tty, withoutLogin bool, lab string, acct *labAccount) (bool, error) {
	if lab == "" || acct != nil {
		return true, nil
	}
	fmt.Fprintln(out, "You're not signed in to Astrona. Your progress and results won't be tracked on your Astrona account. Sign in with: astrona login")
	if withoutLogin {
		return true, nil
	}
	if !tty {
		return false, fmt.Errorf("confirm running without Astrona progress tracking with --without-login, or sign in with `astrona login`")
	}
	return confirmYes(in, out, "Are you sure you want to run this lab without signing in?"), nil
}
