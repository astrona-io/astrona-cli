package portforward

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/ui"
)

// ListenAddress is the only address a forward ever binds — see
// config.PortForward for why it isn't configurable.
const ListenAddress = "127.0.0.1"

// probeTimeout bounds the live TCP check Effective does on a forward its
// supervisor reports Ready.
const probeTimeout = 300 * time.Millisecond

// KubectlArgs builds the argument slice the supervisor runs kubectl with.
// Exported for tests and so the docs/`list -o wide` can show exactly what
// runs.
func KubectlArgs(spec Spec) []string {
	pf := spec.Forward.Normalized()
	return []string{
		"--context", spec.KubeContext,
		"--namespace", pf.Namespace,
		"port-forward",
		"--address", ListenAddress,
		pf.Resource,
		fmt.Sprintf("%d:%d", pf.HostPort, pf.TargetPort),
	}
}

// LocalURL is how a user reaches pf from the host: "http://127.0.0.1:8080"
// for an http(s) forward, "tcp://127.0.0.1:5432" otherwise.
func LocalURL(pf config.PortForward) string {
	pf = pf.Normalized()
	return fmt.Sprintf("%s://%s", pf.Scheme, net.JoinHostPort(ListenAddress, strconv.Itoa(pf.HostPort)))
}

// Effective is f's state as `list` should show it: the supervisor's own
// report, overridden by what can be checked live right now — a dead
// supervisor is Stopped regardless of what status.json last said, and a
// Ready forward whose local port refuses connections is NotReady.
//
// The TCP probe only proves kubectl is listening on the host side, not
// that the pod behind it answers.
func (f Forward) Effective() State {
	if !hypervisor.ProcessAlive(f.PID) {
		return StateStopped
	}
	if f.Status.State == StateReady && !probe(f.Spec.Forward.HostPort) {
		return StateNotReady
	}
	if f.Status.State == "" {
		return StateNotReady
	}
	return f.Status.State
}

func probe(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ListenAddress, strconv.Itoa(port)), probeTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// portFree reports whether port can be bound on ListenAddress right now.
func portFree(port int) error {
	l, err := net.Listen("tcp", net.JoinHostPort(ListenAddress, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("host port %d is already in use on %s: %w", port, ListenAddress, err)
	}
	return l.Close()
}

// Start launches one detached supervisor per forward for the kind cluster
// named lab, replacing any forwards already recorded for it. A forward that
// can't be started (host port taken, …) is reported and skipped rather
// than aborting the rest; the returned error then names every one that
// failed.
func Start(lab string, forwards []config.PortForward, rep *ui.Reporter) error {
	if len(forwards) == 0 {
		return nil
	}
	if err := config.ValidatePortForwards(config.RuntimeConfig{Type: "kind", PortForwards: forwards}); err != nil {
		return err
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl not found in PATH (needed for runtime.portForwards): %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate the astrona binary to run port forward supervisors: %w", err)
	}

	if _, err := Stop(lab); err != nil {
		return fmt.Errorf("failed to stop existing port forwards for '%s': %w", lab, err)
	}

	var errs []error
	for _, raw := range forwards {
		pf := raw.Normalized()
		t := rep.Step("Port forward %s: %s -> %s", pf.Name, LocalURL(pf), pf.Resource)
		if err := startOne(exe, lab, pf); err != nil {
			errs = append(errs, t.Fail(fmt.Errorf("port forward '%s': %w", pf.Name, err)))
			continue
		}
		t.Done()
	}

	return errors.Join(errs...)
}

func startOne(exe, lab string, pf config.PortForward) error {
	if err := portFree(pf.HostPort); err != nil {
		return err
	}

	dir, err := forwardDir(lab, pf.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create state dir '%s': %w", dir, err)
	}

	spec := Spec{Lab: lab, KubeContext: "kind-" + lab, Forward: pf, StartedAt: time.Now()}
	if err := writeJSONAtomic(filepath.Join(dir, specFile), spec); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(dir, statusFile), Status{State: StateNotReady, Since: spec.StartedAt}); err != nil {
		return err
	}

	logF, err := os.OpenFile(filepath.Join(dir, logFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open supervisor log: %w", err)
	}
	defer logF.Close()

	cmd := exec.Command(exe, "port-forward", "supervise", lab, pf.Name)
	cmd.Stdout = logF
	cmd.Stderr = logF
	// Own session/process group: survives `astrona run` exiting, detached
	// from the terminal, and lets Stop signal kubectl along with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start supervisor: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	if err := os.WriteFile(filepath.Join(dir, pidFile), []byte(strconv.Itoa(pid)), 0600); err != nil {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		return fmt.Errorf("failed to record supervisor pid: %w", err)
	}
	return nil
}

// WaitReady polls lab's forwards until every one is Ready or timeout
// passes, and returns them with whatever state they reached. Never an
// error just for not becoming ready — that's the caller's call.
func WaitReady(lab string, timeout time.Duration) ([]Forward, error) {
	deadline := time.Now().Add(timeout)
	for {
		fs, err := List(lab)
		if err != nil {
			return nil, err
		}
		allReady := true
		for _, f := range fs {
			if f.Effective() != StateReady {
				allReady = false
				break
			}
		}
		if allReady || time.Now().After(deadline) {
			return fs, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Stop terminates every forward recorded for lab and removes its state.
// Returns how many forwards were recorded. A lab with none is a no-op.
func Stop(lab string) (int, error) {
	dir, err := labDir(lab)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read port forward state for '%s': %w", lab, err)
	}

	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n++
		terminateSupervisor(readPID(filepath.Join(dir, e.Name())))
	}

	if err := os.RemoveAll(dir); err != nil {
		return n, fmt.Errorf("failed to remove port forward state '%s': %w", dir, err)
	}
	return n, nil
}

// StopAll stops the forwards of every lab that has any. Returns the labs
// stopped.
func StopAll() ([]string, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read port forward state dir '%s': %w", base, err)
	}

	var stopped []string
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || validateLabName(e.Name()) != nil {
			continue
		}
		if _, err := Stop(e.Name()); err != nil {
			errs = append(errs, err)
			continue
		}
		stopped = append(stopped, e.Name())
	}
	return stopped, errors.Join(errs...)
}

// terminateSupervisor SIGTERMs pid's process group (supervisor + its
// kubectl), escalating to SIGKILL after 5s. Only acts when pid is still
// alive *and* still leads its own process group — the supervisor always
// does (Setsid), so a recycled pid now owned by some unrelated process is
// left alone.
func terminateSupervisor(pid int) {
	if pid <= 0 || !hypervisor.ProcessAlive(pid) {
		return
	}
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		return
	}

	_ = syscall.Kill(-pid, syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		if !hypervisor.ProcessAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

// StopForLab is Stop wrapped as a reporter step, for teardown paths: a
// no-op when lab has no forwards, and a failure is only warned about —
// stray forwards must never block deleting the cluster itself.
func StopForLab(lab string, rep *ui.Reporter) {
	if Count(lab) == 0 {
		return
	}
	t := rep.Step("Stop port forwards for %q", lab)
	if _, err := Stop(lab); err != nil {
		t.Skip("failed: %s", err)
		rep.Warn("could not stop port forwards for '%s': %s", lab, err)
		return
	}
	t.Done()
}
