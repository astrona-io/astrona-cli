package labops

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/proctor"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// SubmissionCheck is one check of a Submission.
type SubmissionCheck struct {
	Name       string `json:"name"`
	Pass       bool   `json:"pass"`
	Points     int    `json:"points"`
	Message    string `json:"message,omitempty"`
	Hint       string `json:"hint,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// Submission is a graded result: what `astrona submit -o json` prints and,
// with its texts capped (CapCheckTexts), what is sent to the lab page.
type Submission struct {
	Lab         string            `json:"lab"`
	Pass        bool              `json:"pass"`
	Earned      int               `json:"earned"`
	Max         int               `json:"max"`
	Percent     float64           `json:"percent"`
	PassPercent int               `json:"passPercent,omitempty"`
	OverTime    bool              `json:"overTime,omitempty"`
	Attempt     int               `json:"attempt"`
	Time        time.Time         `json:"time"`
	Checks      []SubmissionCheck `json:"checks"`
}

// NewSubmission builds the Submission for results. Hints are left out when
// the lab hides them (exam conditions).
func NewSubmission(cfg *config.LabConfig, lab string, results []proctor.CheckResult, pass, overTime bool, attempt int, hideHints bool, now time.Time) Submission {
	sc := proctor.ScoreOf(results)
	out := Submission{Lab: lab, Pass: pass, Earned: sc.Earned, Max: sc.Max, Percent: sc.Percent(),
		PassPercent: cfg.Validation.PassPercent, OverTime: overTime, Attempt: attempt, Time: now, Checks: []SubmissionCheck{}}
	for _, r := range results {
		c := SubmissionCheck{Name: r.Name, Pass: r.Pass, Points: r.Points, Message: r.Message, DurationMs: r.Duration.Milliseconds()}
		if !r.Pass && !hideHints {
			c.Hint = r.Hint
		}
		out.Checks = append(out.Checks, c)
	}
	return out
}

// MaxSentCheckText caps each check's message and hint sent to the lab page.
const MaxSentCheckText = 2048

// CapCheckTexts is result as it is sent to the lab page: each check's
// message and hint — script output, possibly huge — without control
// characters (newlines and tabs stay) and capped at MaxSentCheckText
// bytes. The result printed and recorded here is unchanged.
func CapCheckTexts(result Submission) Submission {
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if r == '\n' || r == '\t' || unicode.IsPrint(r) {
				return r
			}
			return -1
		}, s)
		if len(s) <= MaxSentCheckText {
			return s
		}
		cut := MaxSentCheckText
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut] + "…"
	}
	checks := make([]SubmissionCheck, len(result.Checks))
	for i, c := range result.Checks {
		c.Message, c.Hint = clean(c.Message), clean(c.Hint)
		checks[i] = c
	}
	result.Checks = checks
	return result
}

// FindSavedRun finds the saved run started from source, before catalog or
// Git resolution can update its files: its lab name and state, or none.
// Several matching runs are an error.
func FindSavedRun(source *labstate.Source) (string, *labstate.State, error) {
	names, err := labstate.Names()
	if err != nil {
		return "", nil, err
	}
	var name string
	var found *labstate.State
	for _, candidate := range names {
		st, err := labstate.Load(candidate)
		if err != nil {
			return "", nil, err
		}
		if st == nil || !SameSource(st.Source, source) {
			continue
		}
		if found != nil {
			return "", nil, fmt.Errorf("several saved runs match this source; name its config with astrona submit -c <lab-dir>")
		}
		name, found = candidate, st
	}
	return name, found, nil
}

// RunningLabNames lists the running astrona labs the way `astrona list`
// finds them — kind clusters and live qemu VMs — leaving out linked
// clusters (they belong to their lab) and `astrona test` copies. The VMs
// of a multi-VM lab ("<lab>-<vm>") count as their lab when it is
// remembered.
func RunningLabNames() []string {
	qemuRows, _, _ := CollectQEMURows() // an unreadable qemu dir just means no qemu labs here
	kindRows := CollectKindRows()
	owners := LinkedClusterOwners()
	known, _ := labstate.Names()

	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, r := range kindRows {
		if _, linked := owners[r.Name]; linked || strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) || r.Status == "Stopped" {
			continue
		}
		add(r.Name)
	}
	for _, r := range qemuRows {
		if strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) {
			continue
		}
		add(LabOfVM(r.Name, known))
	}
	return names
}

// LabOfVM maps a qemu VM's name to the remembered lab it belongs to: the
// lab itself (single-VM) or the longest remembered lab "<lab>-<vm>"
// starts with; else the VM's own name.
func LabOfVM(vm string, known []string) string {
	best := ""
	for _, k := range known {
		if (vm == k || strings.HasPrefix(vm, k+"-")) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return vm
	}
	return best
}

// PickRunningLab is the lab `astrona submit` grades when none is named:
// the only running one, found again through what `astrona run`
// remembered. No lab running returns no source, so the usual "no lab
// here" error follows; several, or one whose config isn't known, is an
// error saying how to name it.
func PickRunningLab(names []string, load func(string) (*labstate.State, error)) (string, *labstate.Source, error) {
	source := func(name string) *labstate.Source {
		st, err := load(name)
		if err != nil || st == nil {
			return nil
		}
		return st.Source
	}
	switch len(names) {
	case 0:
		return "", nil, nil
	case 1:
		src := source(names[0])
		if src == nil {
			return "", nil, fmt.Errorf("%s is running, but astrona doesn't know which config it was started from "+
				"(it was started by an older astrona) — name it: astrona submit -c <lab-dir>", names[0])
		}
		return names[0], src, nil
	}
	var lines []string
	for _, n := range names {
		lines = append(lines, fmt.Sprintf("  %s    %s", n, SourceHint(source(n))))
	}
	return "", nil, fmt.Errorf("several labs are running — name the one to submit:\n%s", strings.Join(lines, "\n"))
}

// RequireRunningKindLab fails clearly when a kind lab isn't running — or
// when the container engine itself isn't (which hides every lab).
func RequireRunningKindLab(cfg *config.LabConfig, clusterName string) error {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return nil
	}
	if _, err := cluster.DetectContainerEngine(); err != nil {
		return err
	}
	if !cluster.Exists(clusterName) {
		return fmt.Errorf("lab %s isn't running — start it: astrona run", clusterName)
	}
	return nil
}

// StrictOverTime reports whether a passing grade doesn't count because the
// exam's time limit has run out and the lab sets exam.strict — the one
// rule both `submit` and `submit --watch` apply.
func StrictOverTime(examState *exam.State, strict, pass bool, now time.Time) bool {
	return pass && strict && examState != nil && examState.Over(now)
}

// SubmitHistory is `astrona submit --history`: the lab's recorded attempts
// under its cluster name.
func SubmitHistory(cfg *config.LabConfig) (string, []proctor.Attempt, error) {
	clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
	if _, err := labstate.Load(clusterName); err != nil {
		return "", nil, err
	}
	attempts, err := proctor.LoadAttempts(clusterName)
	return clusterName, attempts, err
}

// SubmitRequest is one submission of a loaded lab: the config and where
// it came from, the caller's writers and choices, and the hooks where cmd
// (or the workspace) renders the grade and reacts to its outcome.
type SubmitRequest struct {
	Config  *config.LabConfig
	BaseDir string
	// Lab is where Config came from: the trust check, the source a saved
	// run must match, and the catalog name cleanup messages use.
	Lab LabSource
	// RunID, when set, is the saved run being submitted: a lab re-created
	// since is not graded.
	RunID string
	Trust TrustOptions
	// UI is the reporter options (verbose, plain; Raw is stderr when the
	// caller prints JSON on stdout).
	UI      ui.Options
	NoHints bool
	// Out gets the human grade report and the exam time lines; nil means
	// none (the caller prints the result as JSON).
	Out io.Writer
	// ScriptOut, when set, gets validation scripts' output instead of
	// stdout.
	ScriptOut io.Writer
	// Status gets the sending and cleanup lines; Warn the sending
	// warnings.
	Status, Warn io.Writer
	// Keep keeps the lab after a sent result (--keep).
	Keep bool
	// Destroy is how a finished attempt's lab is deleted.
	Destroy DestroyOptions

	// Graded runs once the attempt is recorded, before the result is sent:
	// cmd writes the JUnit report and prints the result there. Its error
	// ends the submission.
	Graded func(res *SubmitResult, rep *ui.Reporter) error
	// Completed runs once sending is done, before any cleanup.
	Completed func(res *SubmitResult)
	// BeforeCleanup runs just before a sent result deletes the lab (the UI
	// closes its terminals); Deleted runs once the lab is gone.
	BeforeCleanup, Deleted func()
}

// SubmitSession is a lab ready to grade (PrepareSubmit): checked, trusted,
// running, with its Proctor and the "submit" reporter open.
type SubmitSession struct {
	ClusterName string
	Before      *labstate.State // the saved run when grading started
	Proctor     *proctor.Proctor
	Exam        *exam.State // nil unless the lab is a timed exam
	Reporter    *ui.Reporter
}

// SubmitResult is a recorded submission. A failed grade is Passed false,
// never an error.
type SubmitResult struct {
	ClusterName string
	Results     []proctor.CheckResult
	Result      Submission // as `submit -o json` prints it
	Attempt     proctor.Attempt
	Previous    []proctor.Attempt // attempts before this one
	Passed      bool
	Recorded    bool // saved in the local attempt history
	Sent        bool // recorded on the lab page
	// Finished: the attempt is over — its result was sent, or a local-only
	// run's result was saved here.
	Finished bool
}

// PrepareSubmit runs everything before grading: a broken config is an
// error (not a failed grade), the saved run must still be req.RunID, a
// remote lab must be trusted, the lab must be running and its environment
// found. It opens the "submit" reporter (the caller closes it), starts the
// Proctor section and builds the Proctor. `submit --watch` re-grades from
// here; Submit grades once.
func PrepareSubmit(req SubmitRequest) (*SubmitSession, error) {
	cfg := req.Config
	clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
	before, err := labstate.Load(clusterName)
	if err != nil {
		return nil, err
	}
	// A broken config (bad check type, apiVersion…) is an error
	// (exit 1), not a failed grade (exit 2) — and isn't recorded.
	if err := lifecycle.Validate(cfg); err != nil {
		return nil, err
	}
	if req.RunID != "" {
		if err := GuardRun(clusterName, req.RunID); err != nil {
			return nil, err
		}
	}
	// Grading runs validation scripts and command checks on this
	// machine.
	if err := RequireTrust(req.Lab, cfg, req.BaseDir, req.Trust); err != nil {
		return nil, err
	}
	// Grading a lab that isn't there would fail every check (and
	// record that as an attempt) — say what's actually wrong.
	if err := RequireRunningKindLab(cfg, clusterName); err != nil {
		return nil, err
	}

	env, err := runtime.LoadEnvironment(clusterName, cfg.Runtime)
	if err != nil {
		return nil, fmt.Errorf("could not find a running lab environment: %w", err)
	}
	links, _ := lifecycle.Links(clusterName)
	env.WithLinks(links)
	env.AddEnv(lifecycle.CAEnv(clusterName)...)

	rep, err := ui.New("submit", cfg.Metadata.Name, req.UI)
	if err != nil {
		return nil, err
	}
	rep.Section("Proctor")
	var examState *exam.State
	if cfg.Exam.Enabled() {
		if examState, err = exam.Load(clusterName); err != nil {
			rep.Warn("%s", err)
		}
	}
	pr := proctor.NewProctor(req.BaseDir, env)
	if req.NoHints || cfg.Exam.HideHints {
		pr.HideHints()
	}
	return &SubmitSession{ClusterName: clusterName, Before: before, Proctor: pr, Exam: examState, Reporter: rep}, nil
}

// Submit grades a loaded lab once (proctor.Evaluate, rendered on req.Out
// with Proctor.Report), records the attempt, hands it to req.Graded, sends
// it to the lab page (SendResult) and, once the attempt is finished,
// deletes the lab unless req.Keep or teardown.keepCluster. An error means
// the submission could not be graded, or its cleanup failed (the result
// is then already recorded and sent). Cancelling ctx (Ctrl+C) while it
// grades stops before the next check with ErrInterrupted, recording and
// sending nothing; once grading is done, the rest runs to its end.
func Submit(ctx context.Context, req SubmitRequest) (*SubmitResult, error) {
	s, err := PrepareSubmit(req)
	if err != nil {
		return nil, err
	}
	rep := s.Reporter
	defer rep.Close()
	cfg, clusterName, before, pr, examState := req.Config, s.ClusterName, s.Before, s.Proctor, s.Exam
	source := RememberedSource(req.Lab)

	previous, _ := proctor.LoadAttempts(clusterName)
	if req.ScriptOut != nil {
		pr.ScriptOutputTo(req.ScriptOut)
	}
	start := time.Now()
	pr.StopOn(ctx)
	results, pass, err := pr.Evaluate(cfg)
	// An interrupted grade (Ctrl+C) is incomplete: nothing is recorded,
	// sent or cleaned up, and the lab stays as it is.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w: grading stopped, nothing was recorded or sent", ErrInterrupted)
	}
	if err != nil {
		return nil, err
	}
	// Past this point the attempt is recorded and sent as one: a Ctrl+C no
	// longer cuts that short.
	ctx = context.WithoutCancel(ctx)
	if req.Out != nil {
		pr.Report(req.Out, cfg, results, time.Since(start))
	}
	if before != nil {
		if err := GuardRun(clusterName, RunID(before)); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	overTime := false
	if examState != nil {
		if req.Out != nil {
			fmt.Fprintf(req.Out, "Time: %s\n", examState.Summary(now))
		}
		if StrictOverTime(examState, cfg.Exam.Strict, pass, now) {
			pass, overTime = false, true
			if req.Out != nil {
				fmt.Fprintf(req.Out, "Submitted after the time limit — not counted as a pass (exam.strict).\n")
			}
		}
	}

	attempt := proctor.NewAttempt(results, pass, now)
	if examState != nil {
		attempt.Timed = true
		attempt.ElapsedSeconds = int64(examState.Elapsed(now).Seconds())
		attempt.OverTime = examState.Over(now)
	}
	recordErr := proctor.RecordAttempt(clusterName, attempt)
	if recordErr != nil {
		rep.Warn("could not record this attempt: %s", recordErr)
	}
	if recordErr == nil && before != nil && before.StartedAt != "" && SameSource(before.Source, source) && !before.Session.IsPlayground() {
		if err := labstate.SaveSubmission(clusterName, before.StartedAt, pass, now); err != nil {
			rep.Warn("could not mark this run submitted: %s", err)
		}
	}
	res := &SubmitResult{
		ClusterName: clusterName,
		Results:     results,
		Result:      NewSubmission(cfg, clusterName, results, pass, overTime, len(previous)+1, pr.HintsHidden(), now),
		Attempt:     attempt,
		Previous:    previous,
		Passed:      pass,
		Recorded:    recordErr == nil,
	}
	if req.Graded != nil {
		if err := req.Graded(res, rep); err != nil {
			return nil, err
		}
	}
	rep.Close()

	// The grade stands whatever happens to sending it: a failure
	// here is a warning, never a different exit code.
	res.Sent = SendResult(ctx, req.Status, req.Warn, clusterName, source, CapCheckTexts(res.Result))
	if req.Completed != nil {
		req.Completed(res)
	}
	// A sent result finishes the attempt, PASS or FAIL: the lab has no
	// further tracked use, so it goes, as `astrona destroy` would. A local-only
	// run (a UI restart, --local-only) has nothing to send: its result, once
	// saved here, is the final one.
	localRun := before != nil && before.LocalOnly && res.Recorded
	res.Finished = res.Sent || localRun
	if res.Finished && !req.Keep && !cfg.Teardown.KeepCluster {
		name := clusterName
		if req.Lab.Catalog != "" {
			name = req.Lab.Catalog
		}
		if err := func() error {
			if before == nil {
				return fmt.Errorf("the submitted run had no saved identity; the current lab was not deleted")
			}
			if req.BeforeCleanup != nil {
				req.BeforeCleanup()
			}
			opts := req.UI
			opts.Raw = req.Status
			drep, err := ui.New("destroy", cfg.Metadata.Name, opts)
			if err != nil {
				return err
			}
			defer drep.Close()
			info := Teardown{ClusterName: cfg.Metadata.Name, RunID: RunID(before), Status: req.Status, Teardown: cfg.Teardown, Runtime: cfg.Runtime, Config: cfg}
			if err := DestroyLab(info, req.BaseDir, drep, req.Destroy); err != nil {
				return err
			}
			drep.Close()
			fmt.Fprintf(req.Status, "Lab %s deleted.\n", name)
			if req.Deleted != nil {
				req.Deleted()
			}
			return nil
		}(); err != nil {
			return res, fmt.Errorf("result was recorded, but lab cleanup failed: %w; retry with astrona destroy %s", err, name)
		}
	}
	return res, nil
}
