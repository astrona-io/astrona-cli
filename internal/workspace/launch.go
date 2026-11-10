package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// A launch is one `astrona run` that opened the browser before the lab
// exists: the page shows the CLI terms (when they still need accepting) and
// then the lab being set up, and becomes the workspace once the lab is ready.
//
// The CLI owns launches/<id>/launch.json and rewrites it as it goes. The
// server only reads it, and writes the learner's answer to the terms in
// answer.json next to it, which the CLI waits for. The server touches
// launches/<id>/seen whenever the page asks, so the CLI knows whether the
// tab is still open before it opens another one.

// Launch stages, see docs/reference/workspace-api.md.
const (
	LaunchTerms     = "terms"     // waiting for the terms to be accepted or declined
	LaunchChecking  = "checking"  // resolving the lab; the terminal may ask a question
	LaunchPreparing = "preparing" // building the lab
	LaunchReady     = "ready"     // Lab names the run the page opens next
	LaunchFailed    = "failed"    // Error says why; nothing more happens
	LaunchDeclined  = "declined"  // the terms were declined; nothing was started
	LaunchEnded     = "ended"     // the command stopped without a lab for this page (Message says why)
)

// MaxLaunchEvents caps the progress lines kept in a launch.
const MaxLaunchEvents = 200

// launchSchema is the launch.json format version.
const launchSchema = 1

// launchAnswerMu makes checking for an answer and writing one a single step.
var launchAnswerMu sync.Mutex

var launchIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// LaunchTermsNotice is the notice the page shows, exactly as the CLI that
// asks for it ships it (the server may be an older or newer binary).
type LaunchTermsNotice struct {
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Text     string `json:"text"`
}

// LaunchEvent is one line of setup progress.
type LaunchEvent struct {
	At time.Time `json:"at"`
	// Kind is section, step, ok, skip, fail or warn.
	Kind    string  `json:"kind"`
	Label   string  `json:"label"`
	Detail  string  `json:"detail,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
}

// Launch is launch.json.
type Launch struct {
	Schema    int       `json:"schema"`
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Stage     string    `json:"stage"`
	// Message is a short line about the stage, shown under its title.
	Message string             `json:"message,omitempty"`
	Terms   *LaunchTermsNotice `json:"terms,omitempty"`
	Title   string             `json:"title,omitempty"`
	Lab     string             `json:"lab,omitempty"`
	Events  []LaunchEvent      `json:"events"`
	// Status is what the current step is waiting on ("pulling kindest/node · 40s").
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
	LogPath string `json:"logPath,omitempty"`
}

// LaunchAnswer is answer.json: the page's answer to the terms.
type LaunchAnswer struct {
	Accepted bool      `json:"accepted"`
	SHA256   string    `json:"sha256"`
	At       time.Time `json:"at"`
}

// NewLaunchID returns a random launch id.
func NewLaunchID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func launchDir(id string) (string, error) {
	if !launchIDPattern.MatchString(id) {
		return "", errors.New("invalid launch id")
	}
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "launches", id), nil
}

// WriteLaunch saves l for the server.
func WriteLaunch(l Launch) error {
	dir, err := launchDir(l.ID)
	if err != nil {
		return err
	}
	if base, err := StateDir(); err == nil {
		if err := os.MkdirAll(filepath.Join(base, "launches"), 0o700); err != nil {
			return err
		}
		if err := privateDir(filepath.Join(base, "launches")); err != nil {
			return err
		}
	}
	l.Schema = launchSchema
	if len(l.Events) > MaxLaunchEvents {
		l.Events = l.Events[len(l.Events)-MaxLaunchEvents:]
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, "launch.json"), data)
}

// ReadLaunch loads launch id.
func ReadLaunch(id string) (Launch, error) {
	var l Launch
	dir, err := launchDir(id)
	if err != nil {
		return l, err
	}
	data, err := readPrivateFile(filepath.Join(dir, "launch.json"), 1<<20)
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(data, &l); err != nil {
		return l, fmt.Errorf("launch.json: %w", err)
	}
	if l.Schema != launchSchema || l.ID != id {
		return l, errors.New("launch.json is not a launch this version understands")
	}
	return l, nil
}

// ReadLaunchAnswer returns the page's answer to the terms, or
// os.ErrNotExist while there is none.
func ReadLaunchAnswer(id string) (LaunchAnswer, error) {
	var a LaunchAnswer
	dir, err := launchDir(id)
	if err != nil {
		return a, err
	}
	data, err := readPrivateFile(filepath.Join(dir, "answer.json"), 4096)
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return a, fmt.Errorf("answer.json: %w", err)
	}
	return a, nil
}

// LaunchSeen is when the page last asked about launch id (zero if never).
func LaunchSeen(id string) time.Time {
	dir, err := launchDir(id)
	if err != nil {
		return time.Time{}
	}
	info, err := os.Stat(filepath.Join(dir, "seen"))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// RemoveLaunch deletes launch id's files.
func RemoveLaunch(id string) error {
	dir, err := launchDir(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// PruneLaunches removes launches older than maxAge.
func PruneLaunches(maxAge time.Duration) {
	base, err := StateDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(filepath.Join(base, "launches"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !launchIDPattern.MatchString(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > maxAge {
			_ = os.RemoveAll(filepath.Join(base, "launches", e.Name()))
		}
	}
}

// launchView is what GET /api/v1/launches/{id} returns: launch.json plus
// whether the command that wrote it still runs.
type launchView struct {
	Launch
	Alive    bool   `json:"alive"`
	Answered string `json:"answered,omitempty"`
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
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
	if dir, err := launchDir(id); err == nil {
		now := time.Now()
		if err := os.Chtimes(filepath.Join(dir, "seen"), now, now); errors.Is(err, os.ErrNotExist) {
			_ = writePrivateFile(filepath.Join(dir, "seen"), nil)
		}
	}
	v := launchView{Launch: l, Alive: processAlive(l.PID)}
	if a, err := ReadLaunchAnswer(id); err == nil {
		v.Answered = "declined"
		if a.Accepted {
			v.Answered = "accepted"
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleLaunchTerms(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Accept bool   `json:"accept"`
		SHA256 string `json:"sha256"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	l, err := ReadLaunch(id)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeErrorCode(w, http.StatusNotFound, "launch_gone", "this launch is over")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if l.Stage != LaunchTerms || l.Terms == nil || !processAlive(l.PID) {
		writeErrorCode(w, http.StatusConflict, "not_asking", "astrona is not waiting for an answer to the terms")
		return
	}
	if req.SHA256 != l.Terms.SHA256 {
		writeErrorCode(w, http.StatusConflict, "terms_changed", "the terms changed; reload the page")
		return
	}
	dir, err := launchDir(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	launchAnswerMu.Lock()
	defer launchAnswerMu.Unlock()
	if _, err := ReadLaunchAnswer(id); err == nil {
		writeErrorCode(w, http.StatusConflict, "answered", "the terms were already answered")
		return
	}
	data, err := json.Marshal(LaunchAnswer{Accepted: req.Accept, SHA256: req.SHA256, At: time.Now().UTC()})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := writePrivateFile(filepath.Join(dir, "answer.json"), data); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record the answer")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": req.Accept})
}
