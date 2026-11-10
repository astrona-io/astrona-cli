package workspace

import "astrona/internal/labstate"

// Account states, see docs/reference/workspace-api.md.
const (
	AccountSignedIn  = "signed-in"  // the run has a lab session on the student's account
	AccountSignedOut = "signed-out" // a catalog lab run without a session: not signed in
	AccountLocalOnly = "local-only" // a catalog lab run with --local-only
	AccountUntracked = "untracked"  // a lab from the student's own files: no account applies
)

// AccountStatus is what the page may say about the Astrona account a run
// is saved to. `astrona run` decides it from what it did (a lab session or
// none); nothing re-reads the credentials while the page polls. Username
// and site only: never a token, device or key id.
type AccountStatus struct {
	State string `json:"state"`
	// Username is the signed-in student ("" when unknown).
	Username string `json:"username,omitempty"`
	// Site is the CLI's own resolved Astrona site (ASTRONA_URL, the saved
	// sign-in, or the profile's default), never one from lab content.
	Site string `json:"site,omitempty"`
}

// accountFromState is the status of a run recorded without one (astrona
// ui for a run another command started): only what the saved state says.
func accountFromState(st *labstate.State) *AccountStatus {
	switch trackingOf(st) {
	case TrackingAccount:
		return &AccountStatus{State: AccountSignedIn, Site: st.Session.Site}
	case TrackingLocalOnly:
		return &AccountStatus{State: AccountLocalOnly}
	}
	return nil
}
