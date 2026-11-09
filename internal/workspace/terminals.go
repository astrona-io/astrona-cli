package workspace

import (
	"crypto/rand"
	"errors"
	"net/http"
	"os/exec"
	"sort"
	"sync"
	"time"

	"astrona/internal/labshell"
	"astrona/internal/termbridge"
)

// maxTerminalsPerLab caps open terminals (running or ended) per lab.
const maxTerminalsPerLab = 12

// terminal is one shell the page opened: a hardened container from
// labshell.Command on a PTY from termbridge.
type terminal struct {
	id        string
	lab       string
	target    string
	createdAt time.Time
	cmd       *exec.Cmd
	session   *termbridge.Session
	// gone is closed when the terminal is deleted or the server stops, so
	// an attached view closes with 1001 instead of reporting an exit.
	gone     chan struct{}
	goneOnce sync.Once
}

// TerminalInfo describes one terminal to the page and to astrona ui status.
// Lab is only set for ui status.
type TerminalInfo struct {
	ID        string    `json:"id"`
	Lab       string    `json:"lab,omitempty"`
	Target    string    `json:"target"`
	Status    string    `json:"status"`
	ExitCode  *int      `json:"exitCode"`
	CreatedAt time.Time `json:"createdAt"`
	Attached  bool      `json:"attached"`
}

func (t *terminal) ended() bool {
	select {
	case <-t.session.Done():
		return true
	default:
		return false
	}
}

// exitCode is the shell's exit status, -1 for a signal; only after ended.
func (t *terminal) exitCode() int {
	err := t.session.Err()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

func (t *terminal) view() TerminalInfo {
	v := TerminalInfo{ID: t.id, Target: t.target, Status: "running", CreatedAt: t.createdAt.UTC(), Attached: t.session.Attached()}
	if t.ended() {
		code := t.exitCode()
		v.Status, v.ExitCode, v.Attached = "ended", &code, false
	}
	return v
}

func (t *terminal) isGone() bool {
	select {
	case <-t.gone:
		return true
	default:
		return false
	}
}

// close ends the shell and removes its container. Safe to call twice.
func (t *terminal) close() {
	t.goneOnce.Do(func() { close(t.gone) })
	_ = t.session.Close()
	// Cancel removes the named container even after the engine client
	// exited, so a hung-up client cannot leave it running.
	if t.cmd.Cancel != nil {
		_ = t.cmd.Cancel()
	}
}

// terminals is the registry of open terminals.
type terminals struct {
	mu      sync.Mutex
	byID    map[string]*terminal
	pending map[string]int // per lab, terminals being started
	closed  bool
}

func newTerminals() *terminals {
	return &terminals{byID: map[string]*terminal{}, pending: map[string]int{}}
}

func (ts *terminals) get(id string) *terminal {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.byID[id]
}

// list returns the terminals of lab ("" for all), oldest first.
func (ts *terminals) list(lab string) []*terminal {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var out []*terminal
	for _, t := range ts.byID {
		if lab == "" || t.lab == lab {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].createdAt.Before(out[j].createdAt) })
	return out
}

// reserve claims a slot for a new terminal of lab, forgetting the oldest
// ended terminal when the lab is at the cap. It returns false when every
// slot holds a running or starting terminal.
func (ts *terminals) reserve(lab string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.closed {
		return false
	}
	var count int
	var oldestEnded *terminal
	for _, t := range ts.byID {
		if t.lab != lab {
			continue
		}
		count++
		if t.ended() && (oldestEnded == nil || t.createdAt.Before(oldestEnded.createdAt)) {
			oldestEnded = t
		}
	}
	if count+ts.pending[lab] >= maxTerminalsPerLab {
		if oldestEnded == nil {
			return false
		}
		delete(ts.byID, oldestEnded.id)
		go oldestEnded.close()
	}
	ts.pending[lab]++
	return true
}

func (ts *terminals) unreserve(lab string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.pending[lab]--; ts.pending[lab] <= 0 {
		delete(ts.pending, lab)
	}
}

// add registers t in a reserved slot; false when the server is stopping.
func (ts *terminals) add(t *terminal) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.pending[t.lab]--; ts.pending[t.lab] <= 0 {
		delete(ts.pending, t.lab)
	}
	if ts.closed {
		return false
	}
	ts.byID[t.id] = t
	return true
}

func (ts *terminals) remove(id string) *terminal {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t := ts.byID[id]
	delete(ts.byID, id)
	return t
}

// running reports whether any terminal is still running.
func (ts *terminals) running() bool {
	for _, t := range ts.list("") {
		if !t.ended() {
			return true
		}
	}
	return false
}

// closeAll ends every terminal in parallel and refuses new ones.
func (ts *terminals) closeAll() {
	ts.mu.Lock()
	ts.closed = true
	all := make([]*terminal, 0, len(ts.byID))
	for id, t := range ts.byID {
		all = append(all, t)
		delete(ts.byID, id)
	}
	ts.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range all {
		wg.Go(t.close)
	}
	wg.Wait()
}

func TerminalInfos(list []*terminal) []TerminalInfo {
	views := make([]TerminalInfo, 0, len(list))
	for _, t := range list {
		views = append(views, t.view())
	}
	return views
}

func (s *Server) handleTerminals(w http.ResponseWriter, r *http.Request) {
	rec, ok := loadRun(w, r.PathValue("lab"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"terminals": TerminalInfos(s.terms.list(rec.Lab))})
}

func (s *Server) handleOpenTerminal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID  string `json:"runId"`
		Target string `json:"target"`
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
	target := rec.Targets[0].Name
	if req.Target != "" {
		target = ""
		for _, t := range rec.Targets {
			if t.Name == req.Target {
				target = t.Name
			}
		}
		if target == "" {
			writeErrorCode(w, http.StatusBadRequest, "unknown_target", "unknown target")
			return
		}
	}
	v, err := buildView(rec, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch v.Status {
	case StatusStopped:
		writeErrorCode(w, http.StatusConflict, "run_stopped", "the lab is no longer running")
		return
	case StatusReady:
		writeErrorCode(w, http.StatusConflict, "not_started", "click Start first")
		return
	}
	if !s.terms.reserve(rec.Lab) {
		writeErrorCode(w, http.StatusTooManyRequests, "too_many_terminals", "close a terminal first")
		return
	}
	t, err := s.startTerminal(rec.Lab, target)
	if err != nil {
		s.terms.unreserve(rec.Lab)
		if errors.Is(err, termbridge.ErrUnsupported) {
			writeError(w, http.StatusNotImplemented, "terminals are not supported on this platform")
			return
		}
		writeErrorCode(w, http.StatusBadGateway, "terminal_failed", "could not start the terminal: "+err.Error())
		return
	}
	if !s.terms.add(t) {
		go t.close()
		writeError(w, http.StatusServiceUnavailable, "the workspace server is stopping")
		return
	}
	writeJSON(w, http.StatusCreated, t.view())
}

// startTerminal runs labshell's isolated terminal for target. Its context
// is the server's: a request context would kill the shell when the POST
// returns. There is no fallback to a host shell.
func (s *Server) startTerminal(lab, target string) (*terminal, error) {
	cmd, err := labshell.Command(s.life, lab, target, nil, true)
	if err != nil {
		return nil, err
	}
	session, err := termbridge.Start(cmd, termbridge.Options{})
	if err != nil {
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		}
		return nil, err
	}
	id, err := newTerminalID()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return &terminal{id: id, lab: lab, target: target, createdAt: time.Now(), cmd: cmd, session: session, gone: make(chan struct{})}, nil
}

func newTerminalID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b64.EncodeToString(b), nil
}

func (s *Server) handleCloseTerminal(w http.ResponseWriter, r *http.Request) {
	lab := r.PathValue("lab")
	t := s.terms.get(r.PathValue("id"))
	if t == nil || t.lab != lab {
		writeError(w, http.StatusNotFound, "no such terminal")
		return
	}
	s.terms.remove(t.id)
	t.close()
	w.WriteHeader(http.StatusNoContent)
}
