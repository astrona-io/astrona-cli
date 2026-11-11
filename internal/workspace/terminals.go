package workspace

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"sync"
	"time"

	"astrona/internal/labops"
	"astrona/internal/labshell"
	"astrona/internal/termbridge"
)

// maxTerminalsPerLab caps open terminals (running or ended) per lab, across
// the lab's terminal tabs and the ones the learner opened; each tab has one
// terminal.
const maxTerminalsPerLab = 12

// terminal is one shell the page opened for a terminal tab on a PTY from
// termbridge: a hardened container from labshell.Command on a Kind target,
// the student's isolated ssh login (labops.WorkspaceSSHCommand) on a VM.
type terminal struct {
	id        string
	lab       string
	tab       string
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
	Tab       string    `json:"tab"`
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
	v := TerminalInfo{ID: t.id, Tab: t.tab, Target: t.target, Status: "running", CreatedAt: t.createdAt.UTC(), Attached: t.session.Attached()}
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

// close ends the shell and removes its container (a VM's ssh login just
// ends). Safe to call twice.
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
	mu       sync.Mutex
	byID     map[string]*terminal
	pending  map[string]int  // per lab, terminals being started
	starting map[string]bool // per lab and tab, see tabKey
	closed   bool
}

func newTerminals() *terminals {
	return &terminals{byID: map[string]*terminal{}, pending: map[string]int{}, starting: map[string]bool{}}
}

// tabKey keys starting; tab names never contain "/".
func tabKey(lab, tab string) string { return lab + "/" + tab }

// errTabStarting: the tab's terminal is already being started.
var errTabStarting = errors.New("this tab's terminal is starting")

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

// reserve claims the slot of tab's terminal. A tab has one terminal: when
// its shell still runs, reserve returns it and claims nothing; ended
// terminals of the tab are forgotten, so "open a new shell" replaces them.
// When the lab is at the cap the oldest ended terminal is forgotten too.
// It returns ok false when the server is stopping or every slot holds a
// running or starting terminal, and errTabStarting when the tab's terminal
// is being started by another request.
func (ts *terminals) reserve(lab, tab string) (running *terminal, ok bool, err error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.closed {
		return nil, false, nil
	}
	if ts.starting[tabKey(lab, tab)] {
		return nil, false, errTabStarting
	}
	var count int
	var oldestEnded *terminal
	for id, t := range ts.byID {
		if t.lab != lab {
			continue
		}
		if t.tab == tab {
			if !t.ended() {
				return t, true, nil
			}
			delete(ts.byID, id)
			go t.close()
			continue
		}
		count++
		if t.ended() && (oldestEnded == nil || t.createdAt.Before(oldestEnded.createdAt)) {
			oldestEnded = t
		}
	}
	if count+ts.pending[lab] >= maxTerminalsPerLab {
		if oldestEnded == nil {
			return nil, false, nil
		}
		delete(ts.byID, oldestEnded.id)
		go oldestEnded.close()
	}
	ts.pending[lab]++
	ts.starting[tabKey(lab, tab)] = true
	return nil, true, nil
}

func (ts *terminals) unreserve(lab, tab string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.release(lab, tab)
}

// release gives back what reserve claimed; ts.mu must be held.
func (ts *terminals) release(lab, tab string) {
	if ts.pending[lab]--; ts.pending[lab] <= 0 {
		delete(ts.pending, lab)
	}
	delete(ts.starting, tabKey(lab, tab))
}

// add registers t in a reserved slot; false when the server is stopping.
func (ts *terminals) add(t *terminal) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.release(t.lab, t.tab)
	if ts.closed {
		return false
	}
	ts.byID[t.id] = t
	return true
}

// remove forgets the terminal id and returns it (nil when unknown).
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

// handleOpenTerminal opens the shell of a terminal tab: one the lab
// declared, or one the learner opened with "+ New terminal" (a term-<id>
// tab with a target of the run). The page names a tab and at most a
// target of the run, never a command: the shell is the same isolated
// terminal as the focus tab's. A tab whose shell still runs gets that one
// back (200).
func (s *Server) handleOpenTerminal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID  string `json:"runId"`
		Tab    string `json:"tab"`
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
	if req.Tab == "" {
		req.Tab = rec.tabs()[0].Name
	}
	tab, found := rec.tab(req.Tab)
	switch {
	case found && tab.Kind == TabTerminal && (req.Target == "" || req.Target == tab.Target):
	case !found && IsExtraTerminalTab(req.Tab) && req.Target != "":
		if _, ok := rec.target(req.Target); !ok {
			writeErrorCode(w, http.StatusBadRequest, "unknown_target", "this run has no target by that name")
			return
		}
		tab = Tab{Name: req.Tab, Kind: TabTerminal, Target: req.Target}
	default:
		writeErrorCode(w, http.StatusBadRequest, "unknown_tab", "this lab has no terminal tab by that name")
		return
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
	running, ok, err := s.terms.reserve(rec.Lab, tab.Name)
	switch {
	case errors.Is(err, errTabStarting):
		writeErrorCode(w, http.StatusConflict, "terminal_starting", "this tab's terminal is starting; try again in a moment")
		return
	case running != nil:
		writeJSON(w, http.StatusOK, running.view())
		return
	case !ok:
		writeErrorCode(w, http.StatusTooManyRequests, "too_many_terminals", fmt.Sprintf("a lab can have at most %d terminals open; close one to open another", maxTerminalsPerLab))
		return
	}
	target, found := rec.target(tab.Target)
	if !found {
		s.terms.unreserve(rec.Lab, tab.Name)
		writeErrorCode(w, http.StatusBadRequest, "unknown_tab", "this tab's target is not part of the run")
		return
	}
	t, err := s.startTerminal(rec.Lab, tab.Name, target)
	if err != nil {
		s.terms.unreserve(rec.Lab, tab.Name)
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

// startTerminal runs the isolated terminal for target: labshell's tools
// container for a Kind cluster, the student's ssh login for a QEMU VM. Its
// context is the server's: a request context would kill the shell when the
// POST returns. There is no fallback to a host shell.
func (s *Server) startTerminal(lab, tab string, target Target) (*terminal, error) {
	var cmd *exec.Cmd
	var err error
	switch target.Kind {
	case TargetKind:
		cmd, err = labshell.Command(s.life, lab, target.Name, nil, true)
	case TargetVM:
		cmd, err = labops.WorkspaceSSHCommand(s.life, lab, target.Name, s.cfg.PrepareVM)
	default:
		err = fmt.Errorf("target %s has an unknown kind %q", target.Name, target.Kind)
	}
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
	return &terminal{id: id, lab: lab, tab: tab, target: target.Name, createdAt: time.Now(), cmd: cmd, session: session, gone: make(chan struct{})}, nil
}

func newTerminalID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b64.EncodeToString(b), nil
}

// handleCloseTerminal is POST /runs/{lab}/terminals/close: the learner
// closed a terminal tab, so its shell ends (its container is removed; a
// VM's ssh login ends). Files in the lab are kept. Closing a tab without a
// shell is not an error.
func (s *Server) handleCloseTerminal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string `json:"runId"`
		Tab   string `json:"tab"`
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
	if t, found := rec.tab(req.Tab); (!found || t.Kind != TabTerminal) && !IsExtraTerminalTab(req.Tab) {
		writeErrorCode(w, http.StatusBadRequest, "unknown_tab", "this lab has no terminal tab by that name")
		return
	}
	closed, err := s.terms.closeTab(rec.Lab, req.Tab)
	if errors.Is(err, errTabStarting) {
		writeErrorCode(w, http.StatusConflict, "terminal_starting", "this tab's terminal is starting; try again in a moment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"closed": closed})
}

// closeTab ends and forgets every terminal of lab's tab; errTabStarting
// while one is being started.
func (ts *terminals) closeTab(lab, tab string) (int, error) {
	ts.mu.Lock()
	if ts.starting[tabKey(lab, tab)] {
		ts.mu.Unlock()
		return 0, errTabStarting
	}
	var gone []*terminal
	for id, t := range ts.byID {
		if t.lab == lab && t.tab == tab {
			gone = append(gone, t)
			delete(ts.byID, id)
		}
	}
	ts.mu.Unlock()
	for _, t := range gone {
		t.close()
	}
	return len(gone), nil
}
