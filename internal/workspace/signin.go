package workspace

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// Signing in from the launch page: a signed-out `astrona run` of a catalog
// lab or playground offers "Sign in" before Begin. The server runs the
// same sign-in as `astrona login` (labops.SignIn) for the run's own site:
// the page gets the user code and the site's authorize page, never the
// device code or a token, and polls until the student authorized this
// computer there. Begin then starts the run's lab session on that account
// (POST /launches/{id}/session, labops.LinkRun), as a signed-in run would
// have when it started. See docs/reference/workspace-api.md.

// Sign-in states, see docs/reference/workspace-api.md.
const (
	SignInNone        = "none"         // nothing started for this launch
	SignInPending     = "pending"      // waiting for the student on the site's page
	SignInSignedIn    = "signed-in"    // signed in; Begin links the run
	SignInDenied      = "denied"       // declined on the site
	SignInExpired     = "expired"      // the code expired
	SignInDeviceLimit = "device_limit" // the account has too many computers signed in
	SignInCancelled   = "cancelled"    // cancelled on the page
	SignInFailed      = "failed"       // anything else; Message says what
)

const (
	// signInStartTimeout bounds asking the site for a code.
	signInStartTimeout = 30 * time.Second
	// linkTimeout bounds starting the lab session at Begin. It runs under
	// the server's lifetime, not the request's, so a page that goes away
	// mid-call never leaves a session half made.
	linkTimeout = 60 * time.Second
)

// signInJob is one launch's sign-in. Only the server holds it.
type signInJob struct {
	launch    string
	site      string
	state     string
	userCode  string
	verifyURL string
	expiresAt time.Time
	username  string
	message   string
	cancel    context.CancelFunc
}

// signIns are the launches' sign-ins. Only one is pending at a time: they
// all save the same credentials.json.
type signIns struct {
	mu   sync.Mutex
	jobs map[string]*signInJob
	// starting serialises asking the site for a code.
	starting sync.Mutex
}

// signInView is what the sign-in routes return: the code to compare and
// the page to open, never the device code or a token.
type signInView struct {
	State string `json:"state"`
	// Site is the run's Astrona site, the one signed in to.
	Site            string     `json:"site,omitempty"`
	UserCode        string     `json:"userCode,omitempty"`
	VerificationURL string     `json:"verificationUrl,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	Username        string     `json:"username,omitempty"`
	Message         string     `json:"message,omitempty"`
}

func (j *signInJob) view() signInView {
	v := signInView{State: j.state, Site: j.site, Username: j.username, Message: j.message}
	if j.state == SignInPending {
		v.UserCode, v.VerificationURL = j.userCode, j.verifyURL
		at := j.expiresAt.UTC()
		v.ExpiresAt = &at
	}
	return v
}

// job returns launch id's sign-in, nil when none was started.
func (s *signIns) job(id string) *signInJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

// snapshot is launch id's sign-in as the page sees it.
func (s *signIns) snapshot(id string) signInView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[id]; j != nil {
		return j.view()
	}
	return signInView{State: SignInNone}
}

// signedIn is the account launch id signed in to, nil before.
func (s *signIns) signedIn(id string) *AccountStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	if j == nil || j.state != SignInSignedIn {
		return nil
	}
	return &AccountStatus{State: AccountSignedIn, Username: j.username, Site: j.site}
}

// launchForSignIn loads launch id for a sign-in route, writing the error
// when it can't be signed in from.
func launchForSignIn(w http.ResponseWriter, id string) (Launch, bool) {
	l, err := ReadLaunch(id)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeErrorCode(w, http.StatusNotFound, "launch_gone", "this launch is over")
		return l, false
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return l, false
	}
	switch {
	case l.Account == nil || l.Account.State != AccountSignedOut:
		writeErrorCode(w, http.StatusConflict, "signin_not_offered", "this run does not offer signing in")
		return l, false
	case l.Stage == LaunchFailed || l.Stage == LaunchDeclined || l.Stage == LaunchEnded,
		l.Stage != LaunchReady && !processAlive(l.PID):
		writeErrorCode(w, http.StatusConflict, "launch_over", "this launch is over")
		return l, false
	}
	return l, true
}

// handleSignIn is GET /launches/{id}/signin: the launch's sign-in.
func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !launchIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid launch id")
		return
	}
	writeJSON(w, http.StatusOK, s.signIns.snapshot(id))
}

// handleSignInStart is POST /launches/{id}/signin: asks the run's site for
// a code and waits, in the background, for the student to authorize this
// computer. A sign-in already pending or done for the launch is returned
// as it is; an account already signed in on the site is used as is.
func (s *Server) handleSignInStart(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := r.PathValue("id")
	l, ok := launchForSignIn(w, id)
	if !ok {
		return
	}
	if s.cfg.SignInDevice == nil {
		writeErrorCode(w, http.StatusConflict, "signin_unavailable", "this workspace server can't sign in; run astrona login in your terminal")
		return
	}
	site, err := account.NormalizeSite(l.Account.Site)
	if l.Account.Site == "" || err != nil {
		writeErrorCode(w, http.StatusConflict, "signin_unavailable", "the run's Astrona site is not known; run astrona login in your terminal")
		return
	}

	s.signIns.starting.Lock()
	defer s.signIns.starting.Unlock()
	s.signIns.mu.Lock()
	if j := s.signIns.jobs[id]; j != nil && (j.state == SignInPending || j.state == SignInSignedIn) {
		v := j.view()
		s.signIns.mu.Unlock()
		writeJSON(w, http.StatusOK, v)
		return
	}
	for other, j := range s.signIns.jobs {
		if other != id && j.state == SignInPending {
			s.signIns.mu.Unlock()
			writeErrorCode(w, http.StatusConflict, "signin_busy", "another page is signing in; finish or cancel that first")
			return
		}
	}
	s.signIns.mu.Unlock()

	client, store, _, err := labops.AccountDeps()
	if err != nil {
		writeError(w, http.StatusInternalServerError, labops.SanitizeLine(err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), signInStartTimeout)
	defer cancel()
	job := &signInJob{launch: id, site: site}
	if creds, err := client.Active(ctx, store, site); err == nil {
		// Signed in meanwhile (astrona login in a terminal): nothing to ask.
		job.state, job.username = SignInSignedIn, labops.SanitizeLine(creds.Username)
		s.putSignIn(job)
		writeJSON(w, http.StatusOK, s.signIns.snapshot(id))
		return
	}
	signIn, err := labops.BeginSignIn(ctx, os.Stderr, client, store, site, s.cfg.SignInDevice())
	switch {
	case account.IsNotFound(err):
		writeErrorCode(w, http.StatusConflict, "signin_unavailable", site+" doesn't offer astrona sign-in")
		return
	case err != nil:
		writeErrorCode(w, http.StatusBadGateway, "site_unreachable", labops.SanitizeLine(err.Error()))
		return
	}
	pollCtx, pollCancel := context.WithDeadline(s.life, signIn.ExpiresAt().Add(time.Minute))
	job.state = SignInPending
	job.userCode, job.verifyURL, job.expiresAt = signIn.UserCode(), signIn.VerificationURL(), signIn.ExpiresAt()
	job.cancel = pollCancel
	s.putSignIn(job)
	go s.finishSignIn(pollCtx, pollCancel, job, signIn)
	writeJSON(w, http.StatusOK, s.signIns.snapshot(id))
}

func (s *Server) putSignIn(j *signInJob) {
	s.signIns.mu.Lock()
	defer s.signIns.mu.Unlock()
	if s.signIns.jobs == nil {
		s.signIns.jobs = map[string]*signInJob{}
	}
	s.signIns.jobs[j.launch] = j
}

// finishSignIn waits for the student on the site's page and records how
// the sign-in ended. Errors go to the server's log without the codes.
func (s *Server) finishSignIn(ctx context.Context, cancel context.CancelFunc, job *signInJob, signIn *labops.SignIn) {
	defer cancel()
	creds, err := signIn.Finish(ctx, os.Stderr)
	s.signIns.mu.Lock()
	defer s.signIns.mu.Unlock()
	if job.state != SignInPending {
		return // cancelled on the page
	}
	job.cancel, job.userCode, job.verifyURL = nil, "", ""
	switch {
	case err == nil:
		job.state, job.username = SignInSignedIn, labops.SanitizeLine(creds.Username)
	case errors.Is(err, account.ErrAccessDenied):
		job.state, job.message = SignInDenied, "Sign-in was declined in the browser."
	case errors.Is(err, account.ErrDeviceCodeExpired), errors.Is(err, context.DeadlineExceeded):
		job.state, job.message = SignInExpired, "The code expired. Sign in again for a new one."
	case errors.Is(err, account.ErrDeviceLimit):
		job.state, job.message = SignInDeviceLimit, labops.SanitizeLine(err.Error())
	case errors.Is(err, context.Canceled):
		job.state, job.message = SignInCancelled, "Sign-in was cancelled."
	default:
		job.state, job.message = SignInFailed, labops.SanitizeLine(err.Error())
	}
}

// handleSignInCancel is DELETE /launches/{id}/signin: stops waiting for a
// pending sign-in. One that finished is left as it is.
func (s *Server) handleSignInCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !launchIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid launch id")
		return
	}
	s.signIns.mu.Lock()
	j := s.signIns.jobs[id]
	if j != nil && j.state == SignInPending {
		if j.cancel != nil {
			j.cancel()
		}
		j.state, j.message, j.cancel, j.userCode, j.verifyURL = SignInCancelled, "Sign-in was cancelled.", nil, "", ""
	}
	s.signIns.mu.Unlock()
	writeJSON(w, http.StatusOK, s.signIns.snapshot(id))
}

// handleLaunchSession is POST /launches/{id}/session, sent by Begin after
// the student signed in on this launch's page: it starts the ready run's
// lab session on that account (labops.LinkRun) and records the run as
// signed in, so Start then starts its clock on the site as for any
// signed-in run. It answers the run's new runId and account.
func (s *Server) handleLaunchSession(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := r.PathValue("id")
	l, err := ReadLaunch(id)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeErrorCode(w, http.StatusNotFound, "launch_gone", "this launch is over")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if l.Stage != LaunchReady || config.ValidateName(l.Lab) != nil {
		writeErrorCode(w, http.StatusConflict, "not_ready", "the lab is not ready yet")
		return
	}
	job := s.signIns.job(id)
	acct := s.signIns.signedIn(id)
	if job == nil || acct == nil {
		writeErrorCode(w, http.StatusConflict, "not_signed_in", "sign in on this page first")
		return
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	rec, ok := loadRun(w, l.Lab)
	if !ok {
		return
	}
	if rec.Account != nil && rec.Account.State == AccountSignedIn {
		// Already linked (Begin twice, or a reload).
		writeJSON(w, http.StatusOK, map[string]any{"lab": rec.Lab, "runId": rec.RunID, "account": rec.Account})
		return
	}
	if rec.Account == nil || rec.Account.State != AccountSignedOut || rec.CatalogLab == "" {
		writeErrorCode(w, http.StatusConflict, "not_linkable", "this run can't be linked to an account")
		return
	}
	v, err := buildView(rec, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch {
	case v.Status == StatusStopped:
		writeErrorCode(w, http.StatusConflict, "run_stopped", "the lab is no longer running")
		return
	case v.Status != StatusReady && !rec.Playground:
		writeErrorCode(w, http.StatusConflict, "not_linkable", "the run already started; it stays on this computer")
		return
	}
	ctx, cancel := context.WithTimeout(s.life, linkTimeout)
	defer cancel()
	linked, err := labops.LinkRun(ctx, os.Stderr, rec.Lab, job.site, rec.PlaygroundMinutes, func(st *labstate.State) error {
		if st == nil || RunDigest(labops.RunID(st)) != rec.RunID {
			return errRunChanged
		}
		return nil
	})
	if err != nil {
		status, code, msg := linkError(err)
		writeErrorCode(w, status, code, msg)
		return
	}
	st, err := labstate.Load(rec.Lab)
	if err != nil || st == nil {
		writeError(w, http.StatusInternalServerError, "the lab session started, but the run could not be read again; reload")
		return
	}
	rec.RunID = RunDigest(labops.RunID(st))
	rec.Account = &AccountStatus{State: AccountSignedIn, Username: linked.Username, Site: linked.Site}
	if err := WriteRun(rec); err != nil {
		writeError(w, http.StatusInternalServerError, "the lab session started, but the run could not be recorded; reload")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lab": rec.Lab, "runId": rec.RunID, "account": rec.Account})
}

// linkError maps labops.LinkRun's error to the route's answer.
func linkError(err error) (status int, code, msg string) {
	msg = labops.SanitizeLine(err.Error())
	switch {
	case errors.Is(err, errRunChanged):
		return http.StatusConflict, "run_changed", err.Error()
	case errors.Is(err, labops.ErrNotLinkable):
		return http.StatusConflict, "not_linkable", msg
	case errors.Is(err, account.ErrSignedOut):
		return http.StatusConflict, "not_signed_in", "the sign-in is no longer there; sign in again"
	case errors.Is(err, syscall.EWOULDBLOCK):
		return http.StatusConflict, "busy", msg // another command is changing the lab
	}
	return http.StatusBadGateway, "site_unreachable", msg
}
