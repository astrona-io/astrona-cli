package manifests

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/ui"
)

// waitPollInterval is how long to sleep before retrying a gate whose
// target doesn't exist yet.
var waitPollInterval = 2 * time.Second

// notYetMarkers are kubectl errors that mean "the thing to wait for hasn't
// been created yet" — normal right after `kubectl apply` (an operator or
// controller creates it a moment later, a CRD isn't registered yet).
// Anything else, including kubectl's own timeout, fails the gate.
var notYetMarkers = []string{
	"no matching resources found",
	"(NotFound)",
	"doesn't have a resource type",
}

func isNotYet(output string) bool {
	for _, m := range notYetMarkers {
		if strings.Contains(output, m) {
			return true
		}
	}
	return false
}

// WaitFor runs each readiness gate in order against kubeContext, failing
// on the first one that doesn't become ready within its timeout. Each
// failure dumps the target's current state and recent namespace events
// into the step output, so the student/author sees why.
func WaitFor(items []config.WaitFor, kubeContext string, rep *ui.Reporter) error {
	if len(items) == 0 {
		return nil
	}

	kubectlPath, err := executor.LookKubectl()
	if err != nil {
		return err
	}

	for _, w := range items {
		cond := w.EffectiveCondition()
		timeout := w.EffectiveTimeout()
		t := rep.Step("Wait for %s (%s, up to %s)", w.Label(), describeCondition(cond), timeout)
		out := t.Output()

		if err := waitOne(kubectlPath, kubeContext, w, timeout, out); err != nil {
			diagnose(kubectlPath, kubeContext, w, out)
			return t.Fail(fmt.Errorf("'%s' not ready: %w", w.Label(), err))
		}
		t.Done()
	}
	return nil
}

func describeCondition(cond string) string {
	if cond == config.ConditionRollout {
		return "rollout complete"
	}
	return "condition " + cond
}

// waitOne retries kubectl until the gate passes, fails for a real reason,
// or the deadline passes. Only "doesn't exist yet" errors are retried.
func waitOne(kubectlPath, kubeContext string, w config.WaitFor, timeout time.Duration, out io.Writer) error {
	deadline := time.Now().Add(timeout)
	var lastOutput string

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timed out after %s (last: %s)", timeout, lastLine(lastOutput))
		}

		var buf bytes.Buffer
		cmd := exec.Command(kubectlPath, WaitArgs(kubeContext, w, remaining)...)
		cmd.Stdout = io.MultiWriter(out, &buf)
		cmd.Stderr = io.MultiWriter(out, &buf)

		err := cmd.Run()
		if err == nil {
			return nil
		}
		lastOutput = buf.String()
		if !isNotYet(lastOutput) {
			return fmt.Errorf("%w: %s", err, lastLine(lastOutput))
		}

		sleep := waitPollInterval
		if r := time.Until(deadline); r < sleep {
			sleep = r
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
}

// WaitArgs builds the kubectl argument slice for one attempt, with
// remaining as kubectl's own --timeout. Every user-supplied value is its
// own argument (--selector=<v>, --for=condition=<v>), never shell-joined.
func WaitArgs(kubeContext string, w config.WaitFor, remaining time.Duration) []string {
	args := []string{"--context", kubeContext, "--namespace", w.EffectiveNamespace()}
	timeout := fmt.Sprintf("--timeout=%ds", int(math.Max(1, math.Ceil(remaining.Seconds()))))

	target := []string{w.Resource}
	switch {
	case w.Selector != "":
		target = []string{w.Kind(), "--selector=" + w.Selector}
	case w.All:
		target = []string{w.Kind(), "--all"}
	}

	cond := w.EffectiveCondition()
	if cond == config.ConditionRollout {
		args = append(args, "rollout", "status")
		args = append(args, target...)
		return append(args, timeout)
	}
	args = append(args, "wait")
	args = append(args, target...)
	return append(args, "--for=condition="+cond, timeout)
}

// diagnose best-effort prints what the gate's target looks like right now
// plus the namespace's most recent events — usually enough to see an
// ImagePullBackOff, a failing probe, or an unschedulable pod.
func diagnose(kubectlPath, kubeContext string, w config.WaitFor, out io.Writer) {
	get := []string{"--context", kubeContext, "--namespace", w.EffectiveNamespace(), "get"}
	switch {
	case w.Selector != "":
		get = append(get, w.Kind(), "--selector="+w.Selector)
	case w.All:
		get = append(get, w.Kind())
	default:
		get = append(get, w.Resource)
	}
	get = append(get, "-o", "wide")

	fmt.Fprintf(out, "\n--- current state ---\n")
	runTo(out, kubectlPath, get...)

	fmt.Fprintf(out, "\n--- recent events (namespace %s) ---\n", w.EffectiveNamespace())
	var events bytes.Buffer
	runTo(&events, kubectlPath, "--context", kubeContext, "--namespace", w.EffectiveNamespace(),
		"get", "events", "--sort-by=.lastTimestamp")
	lines := strings.Split(strings.TrimRight(events.String(), "\n"), "\n")
	if len(lines) > 16 {
		lines = append(lines[:1], lines[len(lines)-15:]...) // header + newest 15
	}
	fmt.Fprintln(out, strings.Join(lines, "\n"))
}

func runTo(out io.Writer, kubectlPath string, args ...string) {
	cmd := exec.Command(kubectlPath, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	_ = cmd.Run()
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "no output"
	}
	return s
}
