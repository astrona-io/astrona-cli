package portforward

import (
	"astrona/internal/executor"

	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// errorThreshold is how many identical kubectl failures in a row turn
	// NotReady into Error (transient failures never do — see isTransient).
	errorThreshold = 3
	minBackoff     = time.Second
	// maxBackoff stays short: the common failure is "pod not running yet"
	// right after `astrona run`, and a long backoff there just delays Ready
	// well past the moment the pod comes up.
	maxBackoff = 10 * time.Second
	// healthyRun is how long kubectl must have been forwarding before its
	// exit counts as a fresh failure (backoff reset) rather than a crash
	// loop.
	healthyRun = 10 * time.Second
	// maxErrorLen caps how much of a kubectl stderr line ends up in
	// status.json / `list -o wide`.
	maxErrorLen = 256
)

// supervisor owns one forward's status.json. kubectl's stdout/stderr
// scanners and the restart loop all update it, hence the mutex.
type supervisor struct {
	dir  string
	spec Spec
	log  io.Writer

	mu     sync.Mutex
	status Status
}

func (s *supervisor) update(fn func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.status)
	if err := writeJSONAtomic(filepath.Join(s.dir, statusFile), s.status); err != nil {
		fmt.Fprintf(s.log, "%s failed to write status: %s\n", stamp(), err)
	}
}

func (s *supervisor) setState(state State, lastError string) {
	s.update(func(st *Status) {
		if st.State != state {
			st.State = state
			st.Since = time.Now()
		}
		if lastError != "" {
			st.LastError = lastError
		}
	})
}

func stamp() string { return time.Now().Format(time.RFC3339) }

// Supervise runs the restart loop for forward name of lab until ctx is
// cancelled (SIGTERM from Stop) or the lab's kube context disappears (the
// cluster was deleted behind astrona's back). It's the body of the hidden
// `astrona port-forward supervise` command; log is its log file.
func Supervise(ctx context.Context, lab, name string, log io.Writer) error {
	dir, err := forwardDir(lab, name)
	if err != nil {
		return err
	}
	spec, err := loadSpec(dir)
	if err != nil {
		return fmt.Errorf("failed to load port forward spec: %w", err)
	}
	kubectl, err := executor.LookKubectl()
	if err != nil {
		return err
	}

	s := &supervisor{dir: dir, spec: spec, log: log, status: Status{State: StateNotReady, Since: time.Now()}}
	s.update(func(*Status) {})

	args := KubectlArgs(spec)
	fmt.Fprintf(log, "%s supervising: kubectl %s\n", stamp(), strings.Join(args, " "))

	backoff := minBackoff
	prevErr := ""
	repeats := 0

	for {
		started := time.Now()
		wasReady, errLine := s.runOnce(ctx, kubectl, args)

		if ctx.Err() != nil {
			s.setState(StateStopped, "")
			fmt.Fprintf(log, "%s stopped\n", stamp())
			return nil
		}

		if errLine == "" {
			errLine = "kubectl port-forward exited"
		}
		if errLine == prevErr {
			repeats++
		} else {
			prevErr, repeats = errLine, 1
		}
		if wasReady && time.Since(started) >= healthyRun {
			backoff, repeats = minBackoff, 1
		}

		state := StateNotReady
		if repeats >= errorThreshold && !isTransient(errLine) {
			state = StateError
		}
		s.update(func(st *Status) {
			st.Restarts++
			st.KubectlPID = 0
		})
		s.setState(state, errLine)
		fmt.Fprintf(log, "%s kubectl exited (%s), restarting in %s\n", stamp(), errLine, backoff)

		if !kubeContextExists(ctx, kubectl, spec.KubeContext) {
			msg := fmt.Sprintf("kube context '%s' no longer exists — cluster deleted?", spec.KubeContext)
			s.setState(StateStopped, msg)
			fmt.Fprintf(log, "%s %s, giving up\n", stamp(), msg)
			return errors.New(msg)
		}

		select {
		case <-ctx.Done():
			s.setState(StateStopped, "")
			return nil
		case <-time.After(backoff):
		}
		backoff = nextBackoff(backoff)
	}
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// runOnce runs kubectl until it exits. Reports whether it reached Ready
// and the last thing it said on stderr (its exit reason, almost always).
func (s *supervisor) runOnce(ctx context.Context, kubectl string, args []string) (wasReady bool, lastErr string) {
	cmd := exec.CommandContext(ctx, kubectl, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err.Error()
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return false, err.Error()
	}
	if err := cmd.Start(); err != nil {
		return false, truncate(err.Error())
	}
	s.update(func(st *Status) { st.KubectlPID = cmd.Process.Pid })

	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			switch classifyStdout(line) {
			case lineForwarding:
				mu.Lock()
				wasReady = true
				mu.Unlock()
				s.update(func(st *Status) { st.LastError = "" })
				s.setState(StateReady, "")
				fmt.Fprintf(s.log, "%s ready: %s\n", stamp(), line)
			case lineConnection:
				// One per client connection — far too chatty to log.
			default:
				if line != "" {
					fmt.Fprintf(s.log, "%s kubectl: %s\n", stamp(), line)
				}
			}
		}
	}()

	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			fmt.Fprintf(s.log, "%s kubectl: %s\n", stamp(), line)
			mu.Lock()
			lastErr = truncate(line)
			mu.Unlock()
			// Recorded while still forwarding too (e.g. "connection
			// refused" to the pod) — kubectl stays up, but `list -o wide`
			// should show why requests fail.
			s.update(func(st *Status) { st.LastError = truncate(line) })
		}
	}()

	wg.Wait()
	waitErr := cmd.Wait()

	mu.Lock()
	defer mu.Unlock()
	if lastErr == "" && waitErr != nil {
		lastErr = truncate(waitErr.Error())
	}
	return wasReady, lastErr
}

type stdoutLine int

const (
	lineOther stdoutLine = iota
	lineForwarding
	lineConnection
)

// classifyStdout recognizes the two lines kubectl port-forward prints on
// stdout: "Forwarding from 127.0.0.1:8080 -> 80" once listening, and
// "Handling connection for 8080" per client connection.
func classifyStdout(line string) stdoutLine {
	switch {
	case strings.HasPrefix(line, "Forwarding from "):
		return lineForwarding
	case strings.HasPrefix(line, "Handling connection for "):
		return lineConnection
	default:
		return lineOther
	}
}

// transientErrors are kubectl failures that just mean "not yet" — a pod
// still starting/pulling, a pod being replaced — however often they
// repeat. Anything else (service not found, forbidden, …) that keeps
// repeating is reported as Error.
var transientErrors = []string{
	"pod is not running",
	"lost connection to pod",
	"no endpoints",
	"does not have any active endpoints",
	"timed out waiting",
	"connection refused",
	"container not running",
}

func isTransient(errLine string) bool {
	lower := strings.ToLower(errLine)
	for _, t := range transientErrors {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

func truncate(s string) string {
	if len(s) <= maxErrorLen {
		return s
	}
	return s[:maxErrorLen] + "…"
}

// kubeContextExists checks the user's kubeconfig still has kubeContext —
// `kind delete cluster` removes it, so its absence means there's nothing
// left to forward to. An error running kubectl is treated as "exists", so
// a transient failure never makes the supervisor give up.
func kubeContextExists(ctx context.Context, kubectl, kubeContext string) bool {
	out, err := exec.CommandContext(ctx, kubectl, "config", "get-contexts", "-o", "name").Output()
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == kubeContext {
			return true
		}
	}
	return false
}
