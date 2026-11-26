package labops

import (
	"context"
	"errors"
	"fmt"
	"io"

	"astrona/internal/account"
	"astrona/internal/labstate"
)

// ErrNotLinkable: the run can't be linked to an account after it started —
// it already has a lab session, runs --local-only, or is not a catalog lab.
var ErrNotLinkable = errors.New("this run can't be linked to an account")

// LinkedRun is what LinkRun did.
type LinkedRun struct {
	Session  *labstate.Session
	Username string
	Site     string
}

// LinkRun starts the lab session a signed-out run of a catalog lab did not
// get when it started, for the account now signed in on site, under the
// lab's mutation lock — the browser workspace's Begin after the student
// signed in on the launch page. check, when not nil, is run on the saved
// state under the lock first (the run-identity check); its error is
// returned as is. A playground's session gets playgroundMinutes (the lab's
// metadata.timeLimitMinutes, which run recorded), so its clock starts now.
// The session is saved with the run, then the lab ends as a fresh run's
// does once it is ready (LabStart.Done): the lab page is told it is ready,
// the usage reporter starts and a playground's watchdog. Notices and
// warnings go to warn; nothing else is printed.
func LinkRun(ctx context.Context, warn io.Writer, clusterName, site string, playgroundMinutes int, check func(*labstate.State) error) (*LinkedRun, error) {
	release, err := labstate.LockMutation(clusterName)
	if err != nil {
		return nil, err
	}
	defer release()
	st, err := labstate.Load(clusterName)
	if err != nil {
		return nil, err
	}
	if check != nil {
		if err := check(st); err != nil {
			return nil, err
		}
	}
	switch {
	case st == nil:
		return nil, fmt.Errorf("%w: astrona remembers no run of it", ErrNotLinkable)
	case st.LocalOnly:
		return nil, fmt.Errorf("%w: it runs --local-only", ErrNotLinkable)
	case st.Session != nil:
		return nil, fmt.Errorf("%w: it already has a lab session", ErrNotLinkable)
	case st.Source == nil || st.Source.Catalog == "":
		return nil, fmt.Errorf("%w: it is not a lab from the Astrona catalog", ErrNotLinkable)
	}
	lab := st.Source.Catalog
	client, store, _, err := AccountDeps()
	if err != nil {
		return nil, err
	}
	// The site the run resolved when it started, not this process's: the
	// student signed in to it on the launch page.
	creds, err := client.Active(ctx, store, site)
	if err != nil {
		return nil, fmt.Errorf("the sign-in can't be used: %w", err) // errors.Is account.ErrSignedOut when there is none
	}
	opt := account.SessionOptions{}
	if IsPlaygroundName(lab) {
		if playgroundMinutes <= 0 {
			return nil, fmt.Errorf("%w: the playground's time limit is not known — run it again", ErrNotLinkable)
		}
		opt.Kind, opt.TimeLimitMinutes = "playground", playgroundMinutes
	}
	page, sess, err := StartSession(ctx, io.Discard, warn, &Account{Client: client, Store: store, Creds: creds}, "run", lab, opt)
	if err != nil {
		return nil, err
	}
	if err := labstate.Update(clusterName, func(s *labstate.State) {
		s.Session = sess
		s.Guest = false
	}); err != nil {
		// The site has a session the lab doesn't know about: close it
		// rather than leave it open.
		ls := &LabStart{Page: page, Session: sess, out: io.Discard, warn: warn, interrupted: true}
		ls.Abandon()
		return nil, fmt.Errorf("could not save the lab session: %w", err)
	}
	ls := &LabStart{Page: page, Session: sess, out: io.Discard, warn: warn}
	ls.Done(clusterName)
	return &LinkedRun{Session: sess, Username: SanitizeLine(creds.Username), Site: creds.Site}, nil
}
