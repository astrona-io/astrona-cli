// Package proctor grades a lab: Evaluate runs its checks and validation
// scripts and returns the results without printing; Report renders them
// as the human report, and Grade is Evaluate followed by Report on
// stdout. Attempts are recorded per lab (RecordAttempt, LoadAttempts).
package proctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"
)

// CheckResult is the outcome of a single check: did it pass, what did the
// underlying command say, and how long it took — Duration feeds both the
// pytest-style summary line and the JUnit XML report (junit.go).
type CheckResult struct {
	Name     string
	Pass     bool
	Message  string
	Duration time.Duration
	Hint     string // shown on failure (unless hints are hidden)
	Points   int    // weight in the score, >= 1
	// VM is the qemu VM the result was graded on, in a multi-VM lab (its
	// name is then also suffixed "(vm)"); empty otherwise.
	VM string
}

// Score is a submission's weighted result.
type Score struct {
	Earned, Max int
}

// Percent is Earned/Max as 0–100 (0 when nothing was graded).
func (s Score) Percent() float64 {
	if s.Max == 0 {
		return 0
	}
	return 100 * float64(s.Earned) / float64(s.Max)
}

// ScoreOf sums results' points.
func ScoreOf(results []CheckResult) Score {
	var s Score
	for _, r := range results {
		pts := config.EffectivePoints(r.Points)
		s.Max += pts
		if r.Pass {
			s.Earned += pts
		}
	}
	return s
}

// Passed applies a lab's pass rule: with passPercent set, the score must
// reach it; otherwise every result must pass. No results never passes.
func Passed(results []CheckResult, passPercent int) bool {
	if len(results) == 0 {
		return false
	}
	if passPercent > 0 {
		return ScoreOf(results).Percent() >= float64(passPercent)
	}
	for _, r := range results {
		if !r.Pass {
			return false
		}
	}
	return true
}

// Proctor is the sole authority that grades a lab. Student-facing
// `astrona submit` and lab-developer `astrona test` both submit to
// a Proctor instead of running checks themselves — the same way a real exam
// is graded by a proctor, not by the person taking it. Today the Proctor
// still runs on the student's own machine (there is no remote grading
// service yet), but keeping it as one component with a single entry point
// (Grade) means no other command reads validation.checks/validation.script
// directly, and it gives a real remote Proctor service a clean seam to
// slot into later without changing how lab/dev commands call it.
type Proctor struct {
	baseDir   string
	env       *runtime.LabEnvironment
	hideHints bool
	// scriptOut receives validation scripts' output (os.Stdout unless
	// Quiet). podReadyTimeout bounds podReady checks (60s unless Quiet).
	scriptOut       io.Writer
	podReadyTimeout time.Duration
	quiet           bool
	// stop, when set, ends grading before its next check or script once
	// it is cancelled (StopOn).
	stop context.Context
	// progress, when set, hears about each check as Evaluate runs it
	// (ReportProgress); step is the index of the current one.
	progress Progress
	step     int
}

// Progress follows an Evaluate: Planned once with every check's and
// validation script's name in grading order, then Started and Finished for
// each, by its index in that list. Grading that stops on an error leaves
// the rest unfinished.
type Progress interface {
	Planned(names []string)
	Started(i int)
	Finished(i int, r CheckResult)
}

// ReportProgress sends Evaluate's progress to pr (the browser workspace
// shows it while grading).
func (p *Proctor) ReportProgress(pr Progress) { p.progress = pr }

func (p *Proctor) started() {
	if p.progress != nil {
		p.progress.Started(p.step)
	}
}

func (p *Proctor) finished(r CheckResult) {
	if p.progress != nil {
		p.progress.Finished(p.step, r)
	}
	p.step++
}

// planned lists the names Evaluate grades for cfg, in its order: the
// root checks, then the validation scripts and each VM's own checks as
// gradeScripts runs them.
func (p *Proctor) planned(cfg *config.LabConfig) []string {
	var names []string
	for _, c := range cfg.Validation.Checks {
		names = append(names, c.Name)
	}
	scripts := func(val config.ValidationConfig, vmSuffix string) {
		for _, s := range validationScripts(val) {
			if s.Source != "" {
				names = append(names, scriptName(s, vmSuffix))
			}
		}
	}
	own := func(val *config.ValidationConfig, vmSuffix string) {
		if val == nil {
			return
		}
		for _, c := range val.Checks {
			names = append(names, vmName(c.Name, vmSuffix))
		}
		scripts(*val, vmSuffix)
	}
	if p.env.Executor != nil {
		scripts(cfg.Validation, "")
		if vm := p.singleVM(cfg); vm != nil {
			own(vm.Validation, "")
		}
		return names
	}
	for _, vm := range cfg.Runtime.QEMU {
		scripts(cfg.Validation, vm.Name)
		own(vm.Validation, vm.Name)
	}
	return names
}

// singleVM is the one runtime.qemu entry of a single-VM qemu lab (graded
// through env.Executor), nil for any other lab.
func (p *Proctor) singleVM(cfg *config.LabConfig) *config.QEMUVM {
	if p.env.Type != runtime.RuntimeQEMU || len(cfg.Runtime.QEMU) != 1 {
		return nil
	}
	return &cfg.Runtime.QEMU[0]
}

// StopOn makes Evaluate give up before its next check or validation
// script once ctx is cancelled (Ctrl+C), returning ctx's error — the
// grade is then incomplete and must not be recorded.
func (p *Proctor) StopOn(ctx context.Context) { p.stop = ctx }

// stopped is the cancelled StopOn context's error (nil: carry on).
func (p *Proctor) stopped() error {
	if p.stop == nil {
		return nil
	}
	return p.stop.Err()
}

// Quiet makes Evaluate suitable for repeated, live re-grading (`submit
// --watch`): validation script output is discarded and podReady checks
// give up after 2s instead of waiting up to a minute (or their timeout).
func (p *Proctor) Quiet() {
	p.scriptOut = io.Discard
	p.podReadyTimeout = 2 * time.Second
	p.quiet = true
}

// ScriptOutputTo sends validation scripts' output to w instead of stdout —
// e.g. stderr, when stdout carries a JSON result.
func (p *Proctor) ScriptOutputTo(w io.Writer) { p.scriptOut = w }

// HideHints suppresses failed checks' hints in Grade's report (e.g. exam
// conditions).
func (p *Proctor) HideHints() { p.hideHints = true }

// NewProctor builds a Proctor scoped to a single lab run: baseDir resolves
// relative script paths, env provides both the kubectl context every
// declarative check is pinned to (env.KubeContext) and, via gradeScripts,
// whichever executor(s) run the validation script(s) — bash on the host for
// kind, SSH into a VM for qemu (every VM in turn for a multi-VM lab).
func NewProctor(baseDir string, env *runtime.LabEnvironment) *Proctor {
	return &Proctor{baseDir: baseDir, env: env, scriptOut: os.Stdout, podReadyTimeout: 60 * time.Second}
}

// Grade runs the lab's declarative checks and validation script(s),
// printing pytest/robot-style per-case PASS/FAIL lines followed by a
// summary line, and returns every case's result (for a JUnit report, see
// junit.go) alongside the Proctor's overall verdict. It is Evaluate
// followed by Report on stdout.
func (p *Proctor) Grade(cfg *config.LabConfig) ([]CheckResult, bool, error) {
	start := time.Now()

	results, pass, err := p.Evaluate(cfg)
	if err != nil {
		return nil, false, err
	}
	p.Report(os.Stdout, cfg, results, time.Since(start))
	return results, pass, nil
}

// Report writes Grade's human report of results to w: a PASS/FAIL line per
// case with its message and (unless hints are hidden) its hint, the
// summary line with elapsed, and the score. Colour follows w.
func (p *Proctor) Report(w io.Writer, cfg *config.LabConfig, results []CheckResult, elapsed time.Duration) {
	passed := 0
	for _, r := range results {
		if r.Pass {
			passed++
		}
		fmt.Fprintf(w, "  %s  %s %s\n", ui.PassFail(w, r.Pass, 4), r.Name, ui.Paint(w, "("+formatDuration(r.Duration)+")", ui.Dim))
		if r.Message != "" {
			fmt.Fprintf(w, "        %s\n", r.Message)
		}
		if !r.Pass && r.Hint != "" && !p.hideHints {
			fmt.Fprintf(w, "        %s %s\n", ui.Paint(w, "hint:", ui.Yellow, ui.Bold), r.Hint)
		}
	}

	failed := len(results) - passed
	summary := fmt.Sprintf("%d passed, %d failed", passed, failed)
	if failed > 0 {
		summary = ui.Paint(w, summary, ui.Red)
	} else {
		summary = ui.Paint(w, summary, ui.Green)
	}
	fmt.Fprintf(w, "\n%s in %s\n", summary, formatDuration(elapsed))

	score := ScoreOf(results)
	line := fmt.Sprintf("Score: %d/%d points (%.0f%%)", score.Earned, score.Max, score.Percent())
	if pp := cfg.Validation.PassPercent; pp > 0 {
		line += fmt.Sprintf(" — pass mark %d%%", pp)
	}
	fmt.Fprintln(w, line)
}

// Evaluate runs every check and validation script and returns the results
// and verdict without printing a report — Grade's engine, also used by
// `submit` (labops.Submit, which renders with Report or as JSON) and
// `submit --watch` to redraw its own live view. Validation scripts' own
// output still goes to the script writer (ScriptOutputTo, Quiet).
//
// An error means grading could not run (nothing to grade, the lab's
// cluster or VM unreachable, a script missing): callers must not record or
// send it as a result.
func (p *Proctor) Evaluate(cfg *config.LabConfig) ([]CheckResult, bool, error) {
	if !cfg.HasGrading() {
		return nil, false, config.ErrNothingToGrade
	}
	p.step = 0
	if p.progress != nil {
		p.progress.Planned(p.planned(cfg))
	}
	results, err := p.runChecks(cfg.Validation.Checks)
	if err != nil {
		return nil, false, fmt.Errorf("Proctor checks failed to run: %w", err)
	}
	scriptResults, err := p.gradeScripts(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("Proctor script failed to run: %w", err)
	}
	results = append(results, scriptResults...)
	if len(results) == 0 {
		return nil, false, fmt.Errorf("Proctor graded nothing: none of this lab's checks or scripts apply to its environment")
	}
	return results, Passed(results, cfg.Validation.PassPercent), nil
}

// HintsHidden reports whether hints are suppressed (HideHints).
func (p *Proctor) HintsHidden() bool { return p.hideHints }

// formatDuration renders a duration the way pytest reports timings:
// fractional seconds, e.g. "0.42s".
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// runChecks runs each declarative check against the cluster. It does not
// stop at the first failure — it collects every result so Grade can report
// everything at once.
func (p *Proctor) runChecks(checks []config.ValidationCheck) ([]CheckResult, error) {
	if len(checks) == 0 {
		return nil, nil
	}

	var kubectlPath string
	reachable := map[checkTarget]bool{}

	results := make([]CheckResult, 0, len(checks))

	for _, c := range checks {
		if err := p.stopped(); err != nil {
			return nil, err
		}
		result := CheckResult{Name: c.Name, Hint: c.Hint, Points: config.EffectivePoints(c.Points)}
		checkStart := time.Now()
		p.started()

		t, err := p.targetFor(c)
		if err != nil {
			result.Message = err.Error()
			result.Duration = time.Since(checkStart)
			results = append(results, result)
			p.finished(result)
			continue
		}

		// Kubernetes checks only ever run against a lab's own isolated
		// kubeconfig and context — never the user's ambient kubeconfig —
		// and only once that cluster answers, so an unreachable cluster
		// is a grading error rather than a learner's failed check.
		var resourceArgs []string
		if config.IsKubernetesCheck(c.Type) {
			if t.context == "" || t.kubeconfig == "" {
				return nil, fmt.Errorf("check '%s' needs a Kubernetes cluster from the lab, and this lab has none with its own kubeconfig (a qemu lab has no cluster; a kind lab started by an older astrona needs `astrona reset`)", c.Name)
			}
			if resourceArgs, err = config.ResourceArgs(c.Resource); err != nil {
				return nil, fmt.Errorf("check '%s': %w", c.Name, err)
			}
			if kubectlPath == "" {
				if kubectlPath, err = executor.LookKubectl(); err != nil {
					return nil, err
				}
			}
			if !reachable[t] {
				ctx, cancel := executor.WithTimeout(reachabilityTimeout)
				out, err := kubectl(ctx, reachabilityTimeout, kubectlPath, t.kubeconfig, "--context", t.context, "--request-timeout=10s", "get", "--raw=/readyz")
				cancel()
				if err != nil {
					return nil, fmt.Errorf("the lab's cluster (%s) isn't reachable, so check '%s' can't be graded: %s", t.context, c.Name, withTimeout(out, err))
				}
				reachable[t] = true
			}
		}

		// Every check is bounded (its timeout, default 1m): past it the
		// command's whole process group is killed and the check fails
		// with "timed out after …". podReady waits for its own
		// --timeout, so its hard limit is that plus a grace period.
		limit := c.EffectiveTimeout()
		podWait := p.podReadyTimeout
		if strings.EqualFold(c.Type, "podready") {
			if c.Timeout != "" && !p.quiet {
				podWait = limit
			}
			limit = podWait + podReadyGrace
		}
		ctx, cancel := executor.WithTimeout(limit)

		switch strings.ToLower(c.Type) {
		case "resourceexists":
			args := append([]string{"--context", t.context, "get"}, resourceArgs...)
			cmd := executor.Command(ctx, kubectlPath, args...)
			cmd.Env = executor.KubeconfigEnv(t.kubeconfig)
			out, err := executor.CombinedOutput(cmd)
			err = executor.Timed(ctx, limit, err)
			result.Pass = err == nil
			result.Message = withTimeout(strings.TrimSpace(string(out)), err)
		case "podready":
			args := append([]string{"--context", t.context, "wait", "--for=condition=Ready", fmt.Sprintf("--timeout=%ds", int(podWait.Seconds()))}, resourceArgs...)
			cmd := executor.Command(ctx, kubectlPath, args...)
			cmd.Env = executor.KubeconfigEnv(t.kubeconfig)
			out, err := executor.CombinedOutput(cmd)
			err = executor.Timed(ctx, limit, err)
			result.Pass = err == nil
			result.Message = withTimeout(strings.TrimSpace(string(out)), err)
		case "command":
			parts := strings.Fields(c.Command)
			if len(parts) == 0 {
				result.Pass = false
				result.Message = "empty command"
				break
			}

			cmd := executor.Command(ctx, parts[0], parts[1:]...)
			// Same KUBECONFIG lab scripts get, so a `kubectl ...` command
			// check grades the lab's cluster, not the user's own context —
			// or the linked cluster's, for a check with cluster.
			cmd.Env = executor.Env(t.kubeconfig, p.env.ExtraEnv)
			out, err := executor.CombinedOutput(cmd)
			err = executor.Timed(ctx, limit, err)
			trimmed := strings.TrimSpace(string(out))
			if executor.IsTimeout(err) {
				result.Message = withTimeout(trimmed, err)
				break
			}

			ok, why := matchOutput(c, trimmed)
			result.Pass = err == nil && ok
			result.Message = joinMessage(trimmed, why)
		case "jsonpath":
			args := append([]string{"--context", t.context, "get"}, resourceArgs...)
			args = append(args, "-o", "jsonpath="+c.JSONPath)
			out, err := kubectl(ctx, limit, kubectlPath, t.kubeconfig, args...)
			if err != nil {
				result.Message = withTimeout(out, err)
				break
			}
			ok, why := matchOutput(c, out)
			result.Pass = ok
			if !ok {
				result.Message = fmt.Sprintf("%s is %q — %s", c.JSONPath, out, why)
			}
		case "count":
			args := append([]string{"--context", t.context, "get"}, resourceArgs...)
			out, err := kubectl(ctx, limit, kubectlPath, t.kubeconfig, append(args, "-o", "name")...)
			if err != nil {
				result.Message = withTimeout(out, err)
				break
			}
			n := countLines(out)
			result.Pass = withinBounds(n, c.Min, c.Max)
			result.Message = fmt.Sprintf("found %d, want %s", n, describeBounds(c.Min, c.Max))
		case "http":
			result.Pass, result.Message = httpCheck(c)
		default:
			result.Pass = false
			result.Message = fmt.Sprintf("unsupported check type '%s'", c.Type)
		}
		cancel()

		result.Duration = time.Since(checkStart)
		results = append(results, result)
		p.finished(result)
	}

	return results, nil
}

// checkTarget is the cluster a check grades: the lab's own, or a linked
// cluster's (runtime.kind.clusters).
type checkTarget struct {
	context, kubeconfig string
}

// targetFor picks c's cluster. A check with cluster grades that linked
// cluster; one that isn't attached (lab started before it was added to
// runtime.kind.clusters) fails rather than silently grading the lab's own.
func (p *Proctor) targetFor(c config.ValidationCheck) (checkTarget, error) {
	if c.Cluster == "" {
		return checkTarget{context: p.env.KubeContext, kubeconfig: p.env.Kubeconfig}, nil
	}
	l, ok := p.env.Link(c.Cluster)
	if !ok {
		return checkTarget{}, fmt.Errorf("linked cluster '%s' isn't running for this lab — `astrona reset` recreates the lab with it", c.Cluster)
	}
	return checkTarget{context: l.Context(), kubeconfig: cluster.ExistingKubeconfig(l.Cluster)}, nil
}

// kubectl runs kubectl with kubeconfig, bounded by ctx (whose deadline is
// limit), and returns trimmed stdout — or, on failure, trimmed
// stdout+stderr as the error detail. A timeout is an
// *executor.TimeoutError.
func kubectl(ctx context.Context, limit time.Duration, kubectlPath, kubeconfig string, args ...string) (string, error) {
	cmd := executor.Command(ctx, kubectlPath, args...)
	cmd.Env = executor.KubeconfigEnv(kubeconfig)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := executor.Timed(ctx, limit, executor.Run(cmd)); err != nil {
		return strings.TrimSpace(stdout.String() + "\n" + stderr.String()), err
	}
	return strings.TrimSpace(stdout.String()), nil
}

const (
	// podReadyGrace is how long past its own --timeout a podReady
	// `kubectl wait` may run before it is killed.
	podReadyGrace = 15 * time.Second
	// reachabilityTimeout bounds the /readyz probe before a lab's first
	// Kubernetes check (kubectl's own --request-timeout is 10s).
	reachabilityTimeout = 30 * time.Second
)

// withTimeout is a check's message: its output, plus "timed out after …"
// when err is a timeout.
func withTimeout(out string, err error) string {
	if !executor.IsTimeout(err) {
		return out
	}
	if out == "" {
		return err.Error()
	}
	return err.Error() + " — output so far: " + out
}

// gradeScripts runs every validation script this lab defines, and each
// qemu VM's own checks: the shared root Validation.Script/Scripts once for
// a single-environment lab (env.Executor != nil) — followed, for a single-VM
// qemu lab, by that VM's own runtime.qemu[0].validation block — or, for a
// multi-VM qemu lab, once per VM (in config.Runtime.QEMU's order): the
// shared root ones again first, then that VM's own nested QEMUVM.Validation
// checks and scripts (runVMBlock). Every multi-VM result's name is suffixed
// "(vmName)" (runValidationBlock) so two VMs running the same shared script
// don't collide in the report — see the design note in runValidationBlock
// for why a shared script's result isn't deduplicated.
func (p *Proctor) gradeScripts(cfg *config.LabConfig) ([]CheckResult, error) {
	if p.env.Executor != nil {
		results, err := p.runValidationBlock(cfg.Validation, p.env.Executor, "")
		if err != nil {
			return nil, err
		}
		if vm := p.singleVM(cfg); vm != nil && vm.Validation != nil {
			own, err := p.runVMBlock(*vm.Validation, p.env.Executor, "")
			if err != nil {
				return nil, err
			}
			results = append(results, own...)
		}
		return results, nil
	}

	var results []CheckResult

	for _, vm := range cfg.Runtime.QEMU {
		executor, err := p.env.ExecutorForVM(vm.Name)
		if err != nil {
			return nil, err
		}

		vmResults, err := p.runValidationBlock(cfg.Validation, executor, vm.Name)
		if err != nil {
			return nil, err
		}
		results = append(results, vmResults...)

		if vm.Validation != nil {
			ownResults, err := p.runVMBlock(*vm.Validation, executor, vm.Name)
			if err != nil {
				return nil, err
			}
			results = append(results, ownResults...)
		}
	}

	return results, nil
}

// runVMBlock grades a VM's own runtime.qemu[].validation block through
// that VM's executor: its checks (runVMChecks), then its scripts.
func (p *Proctor) runVMBlock(val config.ValidationConfig, ex executor.ScriptExecutor, vm string) ([]CheckResult, error) {
	results, err := p.runVMChecks(val.Checks, ex, vm)
	if err != nil {
		return nil, err
	}
	scriptResults, err := p.runValidationBlock(val, ex, vm)
	if err != nil {
		return nil, err
	}
	return append(results, scriptResults...), nil
}

// runVMChecks runs a VM's own checks. A command check runs inside that VM,
// over the same path as its validation scripts (runVMCommand); every other
// type grades as a root check does (an http check from this machine;
// Kubernetes checks are refused, a qemu lab has no cluster). In a multi-VM
// lab each result's name is suffixed "(vm)" and its VM is set.
func (p *Proctor) runVMChecks(checks []config.ValidationCheck, ex executor.ScriptExecutor, vm string) ([]CheckResult, error) {
	var results []CheckResult
	for _, c := range checks {
		c.Name = vmName(c.Name, vm)
		if !strings.EqualFold(c.Type, "command") {
			res, err := p.runChecks([]config.ValidationCheck{c})
			if err != nil {
				return nil, err
			}
			for i := range res {
				res[i].VM = vm
			}
			results = append(results, res...)
			continue
		}
		if err := p.stopped(); err != nil {
			return nil, err
		}
		start := time.Now()
		p.started()
		result, err := p.runVMCommand(c, ex)
		if err != nil {
			return nil, fmt.Errorf("check '%s': %w", c.Name, err)
		}
		result.VM = vm
		result.Duration = time.Since(start)
		results = append(results, result)
		p.finished(result)
	}
	return results, nil
}

// runVMCommand runs a command check inside a VM: its words, each quoted so
// nothing is interpolated, are the one line of a script the VM's executor
// runs there (the same way as a validation script). Output and matchers
// are judged as for a root command check. Only a real execution problem
// (the VM unreachable: ssh exit 255) is an error.
func (p *Proctor) runVMCommand(c config.ValidationCheck, ex executor.ScriptExecutor) (CheckResult, error) {
	result := CheckResult{Name: c.Name, Hint: c.Hint, Points: config.EffectivePoints(c.Points)}
	parts := strings.Fields(c.Command)
	if len(parts) == 0 {
		result.Message = "empty command"
		return result, nil
	}
	quoted := make([]string, len(parts))
	for i, w := range parts {
		quoted[i] = shellQuote(w)
	}
	f, err := os.CreateTemp("", "astrona-check-*.sh")
	if err != nil {
		return result, err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString("exec " + strings.Join(quoted, " ") + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return result, err
	}

	limit := c.EffectiveTimeout()
	ctx, cancel := executor.WithTimeout(limit)
	defer cancel()
	var out strings.Builder
	err = executor.Timed(ctx, limit, ex.RunScript(ctx, f.Name(), &out))
	trimmed := strings.TrimSpace(out.String())
	if executor.IsTimeout(err) {
		result.Message = withTimeout(trimmed, err)
		return result, nil
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return result, err
	}
	ok, why := matchOutput(c, trimmed)
	result.Pass = err == nil && ok
	result.Message = joinMessage(trimmed, why)
	return result, nil
}

// shellQuote single-quotes s for bash.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// vmName is a result's name in a multi-VM lab: "name (vm)".
func vmName(name, vm string) string {
	if vm == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, vm)
}

// runValidationBlock runs val's Script (if set) then Scripts (if any)
// through executor, in that order. vmSuffix, non-empty only for a multi-VM
// lab, is appended to each result's name ("name (vmSuffix)") — a shared
// root Validation block genuinely runs once per VM in that case (not a
// single shared result), since it's exercising each VM's own independent
// state, so each run needs its own distinguishable row in the report.
func (p *Proctor) runValidationBlock(val config.ValidationConfig, executor executor.ScriptExecutor, vmSuffix string) ([]CheckResult, error) {
	var results []CheckResult

	for _, scriptItem := range validationScripts(val) {
		if err := p.stopped(); err != nil {
			return nil, err
		}
		if scriptItem.Source == "" {
			// No script configured: nothing ran, so no result either way.
			continue
		}
		scriptStart := time.Now()
		p.started()
		scriptPass, message, err := p.runScript(&scriptItem, executor)
		if err != nil {
			return nil, err
		}

		result := CheckResult{
			VM:       vmSuffix,
			Name:     scriptName(scriptItem, vmSuffix),
			Pass:     scriptPass,
			Message:  message,
			Duration: time.Since(scriptStart),
			Hint:     scriptItem.Hint,
			Points:   config.EffectivePoints(scriptItem.Points),
		}
		results = append(results, result)
		p.finished(result)
	}

	return results, nil
}

// validationScripts is val's Script (if set) then its Scripts.
func validationScripts(val config.ValidationConfig) []config.ResourceItem {
	if val.Script == nil {
		return val.Scripts
	}
	return append([]config.ResourceItem{*val.Script}, val.Scripts...)
}

// scriptName is a validation script's result name: "name (vmSuffix)" in a
// multi-VM lab.
func scriptName(s config.ResourceItem, vmSuffix string) string {
	name := s.Name
	if name == "" {
		name = "validation script"
	}
	return vmName(name, vmSuffix)
}

// runScript runs a single validation script through executor. Exit code 0
// is a pass, non-zero is a fail — a failing lab is a normal outcome, not a
// Go error, so only a real execution problem (script missing, bash not
// found, SSH unable to reach the VM) is returned as an error. A script
// that runs past its timeout (config.DefaultValidationScriptTimeout unless
// set) is killed with everything it started and fails, with message
// "timed out after …".
func (p *Proctor) runScript(script *config.ResourceItem, ex executor.ScriptExecutor) (bool, string, error) {
	pass, err := p.runScriptErr(script, ex)
	if executor.IsTimeout(err) {
		return false, err.Error(), nil
	}
	return pass, "", err
}

func (p *Proctor) runScriptErr(script *config.ResourceItem, ex executor.ScriptExecutor) (bool, error) {
	if script == nil || script.Source == "" {
		return false, fmt.Errorf("validation script has no source")
	}

	scriptPath := script.Source
	cleanup := func() {}

	switch strings.ToLower(script.Type) {
	case "url":
		tmpPath, clean, err := config.DownloadToTemp(script.Source, "astrona-validate-*.sh", scripts.MaxScriptDownloadBytes)
		if err != nil {
			return false, fmt.Errorf("failed to download validation script from %s: %w", script.Source, err)
		}
		scriptPath = tmpPath
		cleanup = clean
	case "file":
		resolved, err := config.JoinWithinBaseDir(p.baseDir, scriptPath)
		if err != nil {
			return false, fmt.Errorf("failed to resolve validation script path: %w", err)
		}
		scriptPath = resolved
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			return false, fmt.Errorf("validation script does not exist: %s", scriptPath)
		}
	default:
		return false, fmt.Errorf("unsupported type '%s' for validation script (must be 'file' or 'url')", script.Type)
	}
	defer cleanup()

	limit := script.EffectiveTimeout(config.DefaultValidationScriptTimeout)
	ctx, cancel := executor.WithTimeout(limit)
	defer cancel()
	if err := executor.Timed(ctx, limit, ex.RunScript(ctx, scriptPath, p.scriptOut)); err != nil {
		if executor.IsTimeout(err) {
			return false, err
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("failed to run validation script: %w", err)
	}

	return true, nil
}
