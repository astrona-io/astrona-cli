package workspace

import (
	"errors"
	"net/http"
	"os"
	"time"

	"astrona/internal/account"
	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// ExtendMinutes is how much one Extend adds (labops.ExtendMinutes), as
// often as the student asks; never to an exam.
const ExtendMinutes = labops.ExtendMinutes

// canExtend reports whether the page may offer Extend for v: not an exam,
// a run with an Astrona lab session the run's account is signed in to, the
// run going (Start pressed), and a playground with a deadline the site sent.
// It reads only the saved state: polling never asks the site.
func canExtend(rec RunRecord, st *labstate.State, v runView) bool {
	if rec.Exam || v.Status != StatusRunning || trackingOf(st) != TrackingAccount {
		return false
	}
	if v.Account == nil || v.Account.State != AccountSignedIn || (v.Account.Site != "" && v.Account.Site != st.Session.Site) {
		return false
	}
	if labops.ExtendableSession(rec.Lab, st) != nil {
		return false
	}
	if rec.Playground {
		return v.DeadlineAt != nil
	}
	return true
}

// handleExtend is Extend: ExtendMinutes more on the site for the run's lab
// session, the playground's new deadline saved and its watchdog moved
// (labops.ExtendRun), then the run as GET /runs/{lab} shows it.
func (s *Server) handleExtend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string `json:"runId"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rec, ok := loadRun(w, r.PathValue("lab"))
	if !ok {
		return
	}
	if req.RunID != rec.RunID {
		writeErrorCode(w, http.StatusConflict, "run_changed", "this is not the lab's current run; reload")
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
	case rec.Exam:
		writeErrorCode(w, http.StatusConflict, "not_extendable", "an exam's time limit is never extended")
		return
	case v.Status == StatusReady:
		writeErrorCode(w, http.StatusConflict, "not_extendable", "click Start first")
		return
	case v.Status == StatusExpired:
		writeErrorCode(w, http.StatusConflict, "not_extendable", "the time limit already passed")
		return
	case !v.CanExtend:
		writeErrorCode(w, http.StatusConflict, "not_extendable", "this run has no Astrona lab session to add time to")
		return
	}
	_, err = labops.ExtendRun(r.Context(), os.Stderr, rec.Lab, func(st *labstate.State) error {
		if st == nil || RunDigest(labops.RunID(st)) != rec.RunID {
			return errRunChanged
		}
		return nil
	})
	if err != nil {
		status, code, msg := extendError(err)
		writeErrorCode(w, status, code, msg)
		return
	}
	v, err = buildView(rec, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// extendError maps labops.ExtendRun's error to the route's answer.
func extendError(err error) (status int, code, msg string) {
	msg = labops.SanitizeLine(err.Error())
	var refused *account.ExtendRefusedError
	switch {
	case errors.Is(err, errRunChanged):
		return http.StatusConflict, "run_changed", err.Error()
	case errors.Is(err, labops.ErrNotExtendable):
		return http.StatusConflict, "not_extendable", msg
	case errors.Is(err, account.ErrSignedOut):
		return http.StatusConflict, "not_extendable", "you're not signed in to the run's Astrona site; run astrona login, then try again"
	case errors.Is(err, account.ErrSessionGone):
		return http.StatusConflict, "not_extendable", "the Astrona site no longer has this run's lab session"
	case errors.As(err, &refused):
		switch refused.Code {
		case account.ExtendConflict:
			return http.StatusConflict, "conflict", msg
		case account.ExtendRateLimited:
			return http.StatusTooManyRequests, "rate_limited", msg
		}
		// not_started, ended, no_expected_time, or a code this CLI doesn't know.
		return http.StatusConflict, "not_extendable", msg
	}
	return http.StatusBadGateway, "site_unreachable", msg
}
