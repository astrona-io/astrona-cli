package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labops"
	"astrona/internal/labstate"
)

const runRecordSchema = 1

// Run statuses, see docs/reference/workspace-api.md.
const (
	StatusReady   = "ready"
	StatusRunning = "running"
	StatusExpired = "expired"
	StatusStopped = "stopped"
)

// Target is a place where the page can open terminals: the lab's own Kind
// cluster or a linked one.
type Target struct {
	Name  string   `json:"name"`
	Label string   `json:"label"`
	Kind  string   `json:"kind"`
	Views []string `json:"views"`
}

// RunRecord is what `astrona run` hands the server about one run: what the
// page shows, never the solution, validation, credentials or kubeconfigs.
// It is written to runs/<lab>/run.json under StateDir.
type RunRecord struct {
	Schema int    `json:"schema"`
	Lab    string `json:"lab"`
	// RunID is RunDigest of the lab's labops.RunID when the record was
	// written; it no longer matches once the lab is stopped or restarted.
	RunID             string   `json:"runId"`
	Title             string   `json:"title"`
	Exam              bool     `json:"exam"`
	Playground        bool     `json:"playground"`
	TimeLimitSeconds  int64    `json:"timeLimitSeconds,omitempty"`
	Strict            bool     `json:"strict"`
	HintsHidden       bool     `json:"hintsHidden"`
	QuestionTitle     string   `json:"questionTitle"`
	Question          string   `json:"question"`
	Prerequisites     string   `json:"prerequisites,omitempty"`
	EstimatedDuration string   `json:"estimatedDuration,omitempty"`
	Targets           []Target `json:"targets"`
	// Tabs are the right panel's tabs, the focus tab first; empty in a
	// record from before tabs (DefaultTabs then).
	Tabs []Tab `json:"tabs,omitempty"`
	// KeepEnvironment is teardown.keepCluster: a finished submission
	// leaves the environment running.
	KeepEnvironment bool `json:"keepEnvironment,omitempty"`
	// RunAgain is the command that starts this lab again ("" when the
	// source is not known).
	RunAgain  string    `json:"runAgain,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// startMarker records the Start click for one run.
type startMarker struct {
	RunID     string    `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
}

// RunDigest turns a labops.RunID into the opaque runId the page sees.
func RunDigest(runID string) string {
	sum := sha256.Sum256([]byte("astrona-workspace-run-v1\n" + runID))
	return hex.EncodeToString(sum[:])
}

// CurrentRunDigest is RunDigest of lab's saved run, "" when the lab has no
// saved state.
func CurrentRunDigest(lab string) (string, error) {
	st, err := labstate.Load(lab)
	if err != nil || st == nil {
		return "", err
	}
	return RunDigest(labops.RunID(st)), nil
}

func runDir(lab string) (string, error) {
	if err := config.ValidateName(lab); err != nil {
		return "", fmt.Errorf("lab name: %w", err)
	}
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runs", lab), nil
}

// WriteRun saves rec for the server, replacing an earlier record of the lab
// and its Start marker (a new record is a new run).
func WriteRun(rec RunRecord) error {
	dir, err := runDir(rec.Lab)
	if err != nil {
		return err
	}
	if len(rec.Targets) == 0 {
		return errors.New("a run needs at least one target")
	}
	for _, t := range rec.Targets {
		if err := config.ValidateName(t.Name); err != nil {
			return fmt.Errorf("target name: %w", err)
		}
	}
	if err := validateTabs(rec); err != nil {
		return err
	}
	rec.Schema = runRecordSchema
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if base, err := StateDir(); err == nil {
		// runs/ must be private like its parent before a child is created.
		if err := os.MkdirAll(filepath.Join(base, "runs"), 0o700); err != nil {
			return err
		}
		if err := privateDir(base); err != nil {
			return err
		}
		if err := privateDir(filepath.Join(base, "runs")); err != nil {
			return err
		}
	}
	for _, stale := range []string{"start.json", "submission.json"} {
		if err := os.Remove(filepath.Join(dir, stale)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return writePrivateFile(filepath.Join(dir, "run.json"), data)
}

// ReadRun returns the saved record of lab; os.ErrNotExist when there is none.
func ReadRun(lab string) (RunRecord, error) {
	var rec RunRecord
	dir, err := runDir(lab)
	if err != nil {
		return rec, err
	}
	data, err := readPrivateFile(filepath.Join(dir, "run.json"), 1<<20)
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("parse run record of %s: %w", lab, err)
	}
	if rec.Schema != runRecordSchema || rec.Lab != lab {
		return rec, fmt.Errorf("run record of %s is not usable", lab)
	}
	return rec, nil
}

// RecordedLabs lists the labs with a run record, sorted.
func RecordedLabs() ([]string, error) {
	base, err := StateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(base, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var labs []string
	for _, e := range entries {
		if e.IsDir() && config.ValidateName(e.Name()) == nil {
			labs = append(labs, e.Name())
		}
	}
	sort.Strings(labs)
	return labs, nil
}

// MarkStarted records that the run in lab's record already started at
// startedAt, outside the browser: a lab whose exam clock started when it
// became ready (astrona run without the browser). The page then shows it
// running, and Start can never restart its clock.
func MarkStarted(lab string, startedAt time.Time) error {
	rec, err := ReadRun(lab)
	if err != nil {
		return err
	}
	data, err := json.Marshal(startMarker{RunID: rec.RunID, StartedAt: startedAt.UTC()})
	if err != nil {
		return err
	}
	dir, err := runDir(lab)
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, "start.json"), data)
}

func readStart(lab string) (*startMarker, error) {
	dir, err := runDir(lab)
	if err != nil {
		return nil, err
	}
	data, err := readPrivateFile(filepath.Join(dir, "start.json"), 4<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m startMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// runView is GET /runs/{lab}.
type runView struct {
	Lab              string         `json:"lab"`
	RunID            string         `json:"runId"`
	Title            string         `json:"title"`
	Mode             string         `json:"mode"`
	Playground       bool           `json:"playground"`
	Status           string         `json:"status"`
	StartedAt        *time.Time     `json:"startedAt"`
	DeadlineAt       *time.Time     `json:"deadlineAt"`
	TimeLimitSeconds *int64         `json:"timeLimitSeconds"`
	ServerNow        time.Time      `json:"serverNow"`
	Rules            runRules       `json:"rules"`
	Intro            runIntro       `json:"intro"`
	Questions        []questionView `json:"questions"`
	Targets          []Target       `json:"targets"`
	Tabs             []tabView      `json:"tabs"`
	CanSubmit        bool           `json:"canSubmit"`
	Submit           submitView     `json:"submit"`
	RunAgain         string         `json:"runAgain"`
}

// submitView is what ending the run does, for the page to explain before
// it asks: see docs/reference/workspace-api.md.
type submitView struct {
	// Tracking is where a result goes: account, local-only or none.
	Tracking string `json:"tracking"`
	// KeepEnvironment: a finished submission leaves the environment.
	KeepEnvironment bool `json:"keepEnvironment"`
	// Grading: a submission of this run is being graded now.
	Grading bool `json:"grading"`
	// Checks: while Grading, each check's progress in grading order
	// (empty until the checks start).
	Checks []gradeCheck `json:"checks,omitempty"`
	// Submitted: this run has a saved result (GET .../submission).
	Submitted bool `json:"submitted"`
	// Blocked says why Submit is not possible now ("" when it is).
	Blocked string `json:"blocked"`
}

type runRules struct {
	Strict      bool `json:"strict"`
	HintsHidden bool `json:"hintsHidden"`
}

type runIntro struct {
	PrerequisitesHTML string `json:"prerequisitesHtml"`
	EstimatedDuration string `json:"estimatedDuration"`
}

type questionView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Points *int   `json:"points"`
}

func (r RunRecord) mode() string {
	if r.Exam {
		return "exam"
	}
	return "learning"
}

// buildView reads the run's state without changing anything: polling is
// read-only.
func buildView(rec RunRecord, now time.Time) (v runView, err error) {
	v = runView{
		Lab: rec.Lab, RunID: rec.RunID, Title: rec.Title, Mode: rec.mode(), Playground: rec.Playground,
		ServerNow: now.UTC(),
		Rules:     runRules{Strict: rec.Strict, HintsHidden: rec.HintsHidden},
		Questions: []questionView{{ID: "q1", Title: rec.QuestionTitle}},
		Targets:   rec.Targets,
		Tabs:      tabViews(rec.tabs()),
		Submit:    submitView{Tracking: TrackingNone, KeepEnvironment: rec.KeepEnvironment},
		RunAgain:  rec.RunAgain,
	}
	if sub, err := readSubmission(rec); err == nil && sub != nil {
		v.Submit.Submitted = true
	}
	if rec.Exam && rec.TimeLimitSeconds > 0 {
		limit := rec.TimeLimitSeconds
		v.TimeLimitSeconds = &limit
	}
	if !rec.Playground && rec.Prerequisites != "" {
		html, err := renderMarkdown(rec.Prerequisites)
		if err != nil {
			return v, err
		}
		v.Intro.PrerequisitesHTML = html
	}
	if !rec.Playground {
		v.Intro.EstimatedDuration = rec.EstimatedDuration
	}
	st, err := labstate.Load(rec.Lab)
	if err != nil {
		return v, err
	}
	if st == nil || RunDigest(labops.RunID(st)) != rec.RunID {
		v.Status = StatusStopped
		v.Submit.Blocked = "the lab is no longer running"
		return v, nil
	}
	v.Submit.Tracking = trackingOf(st)
	defer func() {
		switch {
		case err != nil:
		case rec.Playground:
			v.Submit.Blocked = "a playground has no checks to grade"
		case v.Status == StatusReady:
			v.Submit.Blocked = "start the lab first"
		default:
			v.CanSubmit = true
		}
	}()
	if rec.Playground {
		v.Status = StatusRunning
		if started, err := time.Parse(time.RFC3339, st.StartedAt); err == nil {
			v.StartedAt = utcPtr(started)
		}
		if st.Session != nil {
			if deadline, err := time.Parse(time.RFC3339, st.Session.DeadlineAt); err == nil {
				v.DeadlineAt = utcPtr(deadline)
				if !now.Before(deadline) {
					v.Status = StatusExpired
				}
			}
		}
		return v, nil
	}
	marker, err := readStart(rec.Lab)
	if err != nil {
		return v, err
	}
	if marker == nil || marker.RunID != rec.RunID {
		v.Status = StatusReady
		return v, nil
	}
	v.Status = StatusRunning
	v.StartedAt = utcPtr(marker.StartedAt)
	if rec.Exam && rec.TimeLimitSeconds > 0 {
		limit := time.Duration(rec.TimeLimitSeconds) * time.Second
		started := marker.StartedAt
		if es, err := exam.Load(rec.Lab); err == nil && es != nil {
			started, limit = es.StartedAt, es.Limit
			v.StartedAt = utcPtr(started)
		}
		v.DeadlineAt = utcPtr(started.Add(limit))
		if now.After(*v.DeadlineAt) {
			v.Status = StatusExpired
		}
	}
	return v, nil
}

func utcPtr(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}

// loadRun reads lab's record, writing the 404 or 500 itself when it fails.
func loadRun(w http.ResponseWriter, lab string) (RunRecord, bool) {
	if config.ValidateName(lab) != nil {
		writeError(w, http.StatusNotFound, "no such run")
		return RunRecord{}, false
	}
	rec, err := ReadRun(lab)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "no such run")
		return rec, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return rec, false
	}
	return rec, true
}

func (s *Server) handleRuns(w http.ResponseWriter, _ *http.Request) {
	labs, err := RecordedLabs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type summary struct {
		Lab        string `json:"lab"`
		Title      string `json:"title"`
		Mode       string `json:"mode"`
		Playground bool   `json:"playground"`
		Status     string `json:"status"`
	}
	runs := []summary{}
	now := time.Now()
	for _, lab := range labs {
		rec, err := ReadRun(lab)
		if err != nil {
			continue
		}
		v, err := buildView(rec, now)
		if err != nil {
			continue
		}
		runs = append(runs, summary{rec.Lab, rec.Title, v.Mode, rec.Playground, v.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	rec, ok := loadRun(w, r.PathValue("lab"))
	if !ok {
		return
	}
	v, err := buildView(rec, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if checks, ok := s.subs.gradingChecks(rec.Lab, rec.RunID); ok {
		v.Submit.Grading, v.CanSubmit = true, false
		v.Submit.Blocked = "the lab is being submitted"
		v.Submit.Checks = checks
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleQuestion(w http.ResponseWriter, r *http.Request) {
	rec, ok := loadRun(w, r.PathValue("lab"))
	if !ok {
		return
	}
	if r.PathValue("id") != "q1" {
		writeError(w, http.StatusNotFound, "no such question")
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
	html, err := renderMarkdown(rec.Question)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "q1", "title": rec.QuestionTitle, "html": html, "points": nil,
		"target": rec.Targets[0].Name, "hints": []string{},
	})
}

// handleStart is the Start button. It is idempotent: the first call writes
// the marker (and starts an exam's clock), later calls return what it set.
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string `json:"runId"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	lab := r.PathValue("lab")
	s.runMu.Lock()
	defer s.runMu.Unlock()
	rec, ok := loadRun(w, lab)
	if !ok {
		return
	}
	if req.RunID != rec.RunID {
		writeErrorCode(w, http.StatusConflict, "run_changed", "this is not the lab's current run; reload")
		return
	}
	now := time.Now()
	v, err := buildView(rec, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if v.Status == StatusStopped {
		writeErrorCode(w, http.StatusConflict, "run_stopped", "the lab is no longer running")
		return
	}
	if rec.Playground || v.Status != StatusReady {
		writeJSON(w, http.StatusOK, v)
		return
	}
	marker := startMarker{RunID: rec.RunID, StartedAt: now.UTC()}
	if rec.Exam && rec.TimeLimitSeconds > 0 {
		// The clock file is what astrona status and submit read.
		if err := exam.Start(lab, time.Duration(rec.TimeLimitSeconds)*time.Second, marker.StartedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	data, err := json.Marshal(marker)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir, err := runDir(lab)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := writePrivateFile(filepath.Join(dir, "start.json"), data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v, err = buildView(rec, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// runsBusy reports whether a lab the workspace was opened for still runs.
func runsBusy() bool {
	labs, err := RecordedLabs()
	if err != nil {
		return false
	}
	for _, lab := range labs {
		rec, err := ReadRun(lab)
		if err != nil {
			continue
		}
		if current, err := CurrentRunDigest(lab); err == nil && current != "" && current == rec.RunID {
			return true
		}
	}
	return false
}
