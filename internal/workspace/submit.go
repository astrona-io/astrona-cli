package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/proctor"
	"astrona/internal/ui"
)

// Tracking says where a run's result goes, see runView.Submit.
const (
	TrackingAccount   = "account"    // the run has an Astrona lab session
	TrackingLocalOnly = "local-only" // astrona run --local-only or a restart
	TrackingNone      = "none"       // no lab session: results stay here
)

const maxSubmitMessages = 40

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// submitCheck is one check in a browser result: never its command,
// message (script output, expected values) or any solution text.
type submitCheck struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Points int    `json:"points"`
	Hint   string `json:"hint,omitempty"`
}

type submitResult struct {
	Pass        bool    `json:"pass"`
	Earned      int     `json:"earned"`
	Max         int     `json:"max"`
	Percent     float64 `json:"percent"`
	PassPercent int     `json:"passPercent,omitempty"`
	OverTime    bool    `json:"overTime"`
	// ElapsedSeconds is the time spent on the run until this submission.
	ElapsedSeconds int64         `json:"elapsedSeconds,omitempty"`
	Attempt        int           `json:"attempt"`
	Time           time.Time     `json:"time"`
	Checks         []submitCheck `json:"checks"`
}

// submission is what POST /submit and GET /submission return once a
// submission is graded. It is saved as runs/<lab>/submission.json.
type submission struct {
	Lab                string       `json:"lab"`
	RunID              string       `json:"runId"`
	Result             submitResult `json:"result"`
	Recorded           bool         `json:"recorded"`
	Sent               bool         `json:"sent"`
	Queued             bool         `json:"queued"`
	Finished           bool         `json:"finished"`
	EnvironmentRemoved bool         `json:"environmentRemoved"`
	Messages           []string     `json:"messages"`
	CleanupError       string       `json:"cleanupError,omitempty"`
}

// submitJob is one submission being graded; a retry with the same
// Idempotency-Key waits for it instead of grading again.
type submitJob struct {
	key      string
	runID    string
	done     chan struct{}
	progress *gradeProgress
	// Set before done is closed.
	sub    *submission
	status int
	code   string
	msg    string
}

func (j *submitJob) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// Check states in submitView.Checks.
const (
	CheckWaiting = "waiting"
	CheckRunning = "running"
	CheckPassed  = "passed"
	CheckFailed  = "failed"
)

// gradeCheck is one check while a submission is graded: its name, state
// and timing only, never its command or output.
type gradeCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// StartedAt is set once the check runs; Seconds once it is done.
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Seconds   *float64   `json:"seconds,omitempty"`
}

// gradeProgress is the proctor.Progress of a submission being graded:
// labops.Submit writes it, GET /runs/{lab} reads it.
type gradeProgress struct {
	mu     sync.Mutex
	checks []gradeCheck
}

func (g *gradeProgress) Planned(names []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checks = make([]gradeCheck, len(names))
	for i, n := range names {
		g.checks[i] = gradeCheck{Name: n, State: CheckWaiting}
	}
}

func (g *gradeProgress) Started(i int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if i >= 0 && i < len(g.checks) {
		now := time.Now().UTC()
		g.checks[i].State, g.checks[i].StartedAt = CheckRunning, &now
	}
}

func (g *gradeProgress) Finished(i int, r proctor.CheckResult) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if i < 0 || i >= len(g.checks) {
		return
	}
	c := &g.checks[i]
	c.State = CheckFailed
	if r.Pass {
		c.State = CheckPassed
	}
	secs := math.Round(r.Duration.Seconds()*100) / 100
	c.Seconds = &secs
}

// snapshot copies the checks for a response; nil before grading starts.
func (g *gradeProgress) snapshot() []gradeCheck {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.checks == nil {
		return nil
	}
	return append([]gradeCheck(nil), g.checks...)
}

// submits holds the latest submission job per lab.
type submits struct {
	mu   sync.Mutex
	jobs map[string]*submitJob
}

func (ss *submits) get(lab string) *submitJob {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.jobs[lab]
}

// grading reports whether lab's run runID is being graded now.
func (ss *submits) grading(lab, runID string) bool {
	j := ss.get(lab)
	return j != nil && j.runID == runID && !j.finished()
}

// gradingChecks is the progress of lab's run runID while it is being
// graded; ok is false when it is not.
func (ss *submits) gradingChecks(lab, runID string) (checks []gradeCheck, ok bool) {
	j := ss.get(lab)
	if j == nil || j.runID != runID || j.finished() {
		return nil, false
	}
	return j.progress.snapshot(), true
}

// closeLab ends every terminal of lab.
func (ts *terminals) closeLab(lab string) {
	var wg sync.WaitGroup
	for _, t := range ts.list(lab) {
		ts.remove(t.id)
		wg.Go(t.close)
	}
	wg.Wait()
}

// trackingOf says where a run's result goes, from its saved state.
func trackingOf(st *labstate.State) string {
	switch {
	case st == nil:
		return TrackingNone
	case st.LocalOnly:
		return TrackingLocalOnly
	case st.Session != nil:
		return TrackingAccount
	}
	return TrackingNone
}

func labSourceOf(src *labstate.Source) labops.LabSource {
	if src == nil {
		return labops.LabSource{File: "config.yaml"}
	}
	out := labops.LabSource{Config: src.Config, File: src.File, Git: src.Git, GitRef: src.GitRef, Catalog: src.Catalog}
	if out.File == "" {
		out.File = "config.yaml"
	}
	return out
}

// checkCurrentRun verifies, under the lab's mutation lock, that runID is
// still the lab's saved run. The lock is released before returning:
// labops.Submit and DestroyLab take it themselves.
func checkCurrentRun(lab, runID string) (*labstate.State, error) {
	release, err := labstate.LockMutation(lab)
	if err != nil {
		return nil, err
	}
	defer release()
	st, err := labstate.Load(lab)
	if err != nil {
		return nil, err
	}
	if st == nil || RunDigest(labops.RunID(st)) != runID {
		return nil, errRunChanged
	}
	return st, nil
}

var errRunChanged = errors.New("this is not the lab's current run; reload")

// lineBuffer collects the status lines labops writes for the page.
type lineBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	log io.Writer
}

func (b *lineBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.log != nil {
		_, _ = b.log.Write(p)
	}
	return b.buf.Write(p)
}

func (b *lineBuffer) lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []string{}
	for _, line := range strings.Split(b.buf.String(), "\n") {
		line = strings.TrimSpace(labops.SanitizeLine(line))
		if line != "" && len(out) < maxSubmitMessages {
			out = append(out, line)
		}
	}
	return out
}

func browserResult(s labops.Submission) submitResult {
	s = labops.CapCheckTexts(s)
	r := submitResult{
		Pass: s.Pass, Earned: s.Earned, Max: s.Max, Percent: s.Percent, PassPercent: s.PassPercent,
		OverTime: s.OverTime, ElapsedSeconds: s.ElapsedSeconds, Attempt: s.Attempt, Time: s.Time.UTC(), Checks: make([]submitCheck, 0, len(s.Checks)),
	}
	for _, c := range s.Checks {
		r.Checks = append(r.Checks, submitCheck{Name: c.Name, Pass: c.Pass, Points: c.Points, Hint: c.Hint})
	}
	return r
}

func writeSubmission(sub *submission) error {
	dir, err := runDir(sub.Lab)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(sub, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, "submission.json"), data)
}

// readSubmission returns the saved submission of rec's run, nil when there
// is none.
func readSubmission(rec RunRecord) (*submission, error) {
	dir, err := runDir(rec.Lab)
	if err != nil {
		return nil, err
	}
	data, err := readPrivateFile(filepath.Join(dir, "submission.json"), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sub submission
	if err := json.Unmarshal(data, &sub); err != nil {
		return nil, fmt.Errorf("parse submission of %s: %w", rec.Lab, err)
	}
	if sub.Lab != rec.Lab || sub.RunID != rec.RunID {
		return nil, nil
	}
	return &sub, nil
}

// handleSubmit grades the run (labops.Submit, as astrona submit does),
// records it, sends it where the run is tracked and, once the attempt is
// finished, removes the environment unless the lab keeps it. The same
// Idempotency-Key grades once: a retry waits for, or replays, that result.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if !idempotencyKeyPattern.MatchString(key) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_idempotency_key", "an Idempotency-Key header of 8 to 128 letters, digits, - or _ is required")
		return
	}
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

	s.subs.mu.Lock()
	job := s.subs.jobs[rec.Lab]
	if job != nil && job.key == key && job.runID == rec.RunID {
		s.subs.mu.Unlock()
		s.replySubmit(w, r, job)
		return
	}
	if job != nil && !job.finished() {
		s.subs.mu.Unlock()
		writeErrorCode(w, http.StatusConflict, "submit_in_progress", "this lab is already being submitted; wait for its result")
		return
	}
	if rec.Playground {
		s.subs.mu.Unlock()
		writeErrorCode(w, http.StatusConflict, "no_grading", "a playground has no checks to grade; end it instead")
		return
	}
	v, err := buildView(rec, time.Now())
	if err != nil {
		s.subs.mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch v.Status {
	case StatusStopped:
		s.subs.mu.Unlock()
		writeErrorCode(w, http.StatusConflict, "run_stopped", "the lab is no longer running")
		return
	case StatusReady:
		s.subs.mu.Unlock()
		writeErrorCode(w, http.StatusConflict, "not_started", "click Start first")
		return
	}
	job = &submitJob{key: key, runID: rec.RunID, done: make(chan struct{}), progress: &gradeProgress{}}
	s.subs.jobs[rec.Lab] = job
	s.subs.mu.Unlock()

	release := s.Hold()
	go func() {
		defer release()
		defer close(job.done)
		s.runSubmit(rec, job)
	}()
	s.replySubmit(w, r, job)
}

// replySubmit waits for job and writes its outcome. A request that goes
// away first leaves the job running; GET /submission reports it.
func (s *Server) replySubmit(w http.ResponseWriter, r *http.Request, job *submitJob) {
	select {
	case <-job.done:
	case <-r.Context().Done():
		return
	}
	if job.sub == nil {
		writeErrorCode(w, job.status, job.code, job.msg)
		return
	}
	writeJSON(w, http.StatusOK, job.sub)
}

func (j *submitJob) fail(status int, code, msg string) {
	j.status, j.code, j.msg = status, code, msg
}

// runSubmit is the grading itself. Output for a terminal (the grade
// report, validation scripts' output) goes to the server log only; the
// status and warning lines are returned to the page.
func (s *Server) runSubmit(rec RunRecord, job *submitJob) {
	st, err := checkCurrentRun(rec.Lab, rec.RunID)
	if errors.Is(err, errRunChanged) {
		job.fail(http.StatusConflict, "run_changed", err.Error())
		return
	}
	if err != nil {
		job.fail(http.StatusConflict, "lab_busy", "the lab is busy with another astrona command: "+err.Error()+". Nothing was graded or sent")
		return
	}
	loaded, err := labops.LoadSavedLab(rec.Lab, labSourceOf(st.Source), labops.LoadOptions{Version: s.cfg.Version})
	if err != nil {
		job.fail(http.StatusInternalServerError, "grading_failed", "could not load the lab's config: "+err.Error()+". Nothing was graded or sent; the lab keeps running")
		return
	}
	defer loaded.Cleanup()

	started, _ := StartedAt(rec.Lab)
	logw := os.Stderr // the detached server's stderr is its log
	status := &lineBuffer{log: logw}
	removed := false
	res, err := labops.Submit(s.life, labops.SubmitRequest{
		Config:    loaded.Config,
		BaseDir:   loaded.BaseDir,
		Lab:       labSourceOf(st.Source),
		RunID:     labops.RunID(st),
		Trust:     browserTrustOptions(status),
		UI:        ui.Options{Plain: true, Screen: logw, Raw: logw},
		Out:       logw,
		ScriptOut: logw,
		Status:    status,
		Warn:      status,
		Progress:  job.progress,
		StartedAt: started,
		Destroy: labops.DestroyOptions{
			Out:        status,
			ForgetWarn: status,
			Forget:     labops.ForgetOptions{EndSession: func(name string) { labops.EndRunSession(status, name) }},
		},
		BeforeCleanup: func() { s.terms.closeLab(rec.Lab) },
		Deleted:       func() { removed = true },
	})
	if res == nil {
		msg := "grading did not run"
		if err != nil {
			msg = err.Error()
		}
		if errors.Is(err, labops.ErrNotTrusted) {
			job.fail(http.StatusConflict, "not_trusted", msg+". Approve the lab with astrona run in a terminal first; nothing was graded or sent and the lab keeps running")
			return
		}
		job.fail(http.StatusBadGateway, "grading_failed", msg+". Nothing was recorded or sent; the lab keeps running")
		return
	}
	sub := &submission{
		Lab: rec.Lab, RunID: rec.RunID, Result: browserResult(res.Result),
		Recorded: res.Recorded, Sent: res.Sent, Queued: res.Queued, Finished: res.Finished, EnvironmentRemoved: removed,
		Messages: status.lines(),
	}
	if err != nil {
		sub.CleanupError = labops.SanitizeLine(err.Error())
	}
	if werr := writeSubmission(sub); werr != nil {
		fmt.Fprintf(logw, "workspace: could not save the submission of %s: %v\n", rec.Lab, werr)
	}
	job.sub = sub
	// Usage events of this submit (and its teardown) go out in the background.
	labops.StartOutboxFlusher()
}

// handleSubmission is the latest submission of the run: being graded,
// failed (until the next submit) or its result. Read-only.
func (s *Server) handleSubmission(w http.ResponseWriter, r *http.Request) {
	rec, ok := loadRun(w, r.PathValue("lab"))
	if !ok {
		return
	}
	if job := s.subs.get(rec.Lab); job != nil && job.runID == rec.RunID {
		if !job.finished() {
			writeJSON(w, http.StatusOK, map[string]any{"state": "grading"})
			return
		}
		if job.sub == nil {
			writeJSON(w, http.StatusOK, map[string]any{"state": "failed", "error": map[string]string{"code": job.code, "message": job.msg}})
			return
		}
	}
	sub, err := readSubmission(rec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sub == nil {
		writeErrorCode(w, http.StatusNotFound, "no_submission", "this run has not been submitted")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "done", "submission": sub})
}

// handleDestroy removes the run's environment without grading, as astrona
// destroy does: a tracked lab's attempt is closed without a result, a
// playground's clock stops. A lab from your own files with
// teardown.keepCluster keeps its cluster (labops.KeepsCluster); a catalog
// or tracked lab is always removed.
func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) {
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
	if s.subs.grading(rec.Lab, rec.RunID) {
		writeErrorCode(w, http.StatusConflict, "submit_in_progress", "this lab is being submitted; wait for its result")
		return
	}
	status := &lineBuffer{log: os.Stderr}
	opts := labops.DestroyOptions{
		Out:        status,
		ForgetWarn: status,
		Forget:     labops.ForgetOptions{EndSession: func(name string) { labops.EndRunSession(status, name) }},
	}
	kept, err := s.destroyRun(rec, opts, status)
	if errors.Is(err, errRunChanged) {
		writeErrorCode(w, http.StatusConflict, "run_changed", err.Error())
		return
	}
	if err != nil {
		writeErrorCode(w, http.StatusBadGateway, "destroy_failed", labops.SanitizeLine(err.Error()))
		return
	}
	if kept {
		writeErrorCode(w, http.StatusConflict, "environment_kept", "this lab keeps its environment (teardown.keepCluster); nothing was removed. Remove it in a terminal with: astrona destroy "+rec.Lab)
		return
	}
	labops.StartOutboxFlusher()
	writeJSON(w, http.StatusOK, map[string]any{"environmentRemoved": true, "messages": status.lines()})
}

// destroyRun removes rec's environment. A lab from your own files with
// teardown.keepCluster is left alone (kept is true): nothing is run or
// removed.
func (s *Server) destroyRun(rec RunRecord, opts labops.DestroyOptions, status io.Writer) (kept bool, err error) {
	rep, err := ui.New("destroy", rec.Lab, ui.Options{Plain: true, Screen: os.Stderr, Raw: os.Stderr})
	if err != nil {
		return false, err
	}
	defer rep.Close()
	if rec.Playground {
		release, err := labstate.LockMutation(rec.Lab)
		if err != nil {
			return false, err
		}
		defer release()
		st, err := labstate.Load(rec.Lab)
		if err != nil {
			return false, err
		}
		if st == nil || RunDigest(labops.RunID(st)) != rec.RunID {
			return false, errRunChanged
		}
		s.terms.closeLab(rec.Lab)
		return false, labops.DestroyByNameLocked(rec.Lab, rep, opts)
	}
	st, err := checkCurrentRun(rec.Lab, rec.RunID)
	if err != nil {
		return false, err
	}
	loaded, err := labops.LoadSavedLab(rec.Lab, labSourceOf(st.Source), labops.LoadOptions{Version: s.cfg.Version})
	if err != nil {
		return false, fmt.Errorf("%w; nothing was removed. Remove it in a terminal with: astrona destroy %s", err, rec.Lab)
	}
	defer loaded.Cleanup()
	cfg := loaded.Config
	info := labops.Teardown{
		ClusterName: cfg.Metadata.Name, RunID: labops.RunID(st), Status: status,
		Teardown: cfg.Teardown, Runtime: cfg.Runtime, Config: cfg,
	}
	if labops.HasTeardownScripts(info) {
		// As astrona destroy: an unapproved lab is removed without its
		// teardown scripts. There is nobody to ask here.
		if err := labops.RequireTrust(labSourceOf(st.Source), cfg, loaded.BaseDir, browserTrustOptions(status)); err != nil {
			fmt.Fprintf(status, "Skipping the lab's teardown scripts (%s); removing it without them.\n", err)
			info.SkipScripts = true
		}
	}
	if labops.KeepsCluster(cfg.Teardown, st) {
		return true, nil
	}
	s.terms.closeLab(rec.Lab)
	return false, labops.DestroyLab(info, loaded.BaseDir, rep, opts)
}

func browserTrustOptions(log io.Writer) labops.TrustOptions {
	return labops.TrustOptions{Log: log, Official: labops.AstronaPublishedPin}
}
