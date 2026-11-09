package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"astrona/internal/sshopts"
)

// ScriptExecutor runs a script that has already been resolved to a local
// path on disk. RunInitScripts/Proctor.runScript own getting the script
// there (local file or downloaded URL) and stay ignorant of where it
// actually executes: LocalExecutor runs it on the host (the kind runtime),
// SSHExecutor runs it inside a VM (the qemu runtime).
//
// out receives the script's combined stdout and stderr; callers pass the
// writer their progress UI hands them (a per-step capture/log sink), so a
// script's chatter only reaches the terminal when the caller wants it to.
//
// ctx bounds the run: when it ends, the script's whole process group is
// killed (see Command). Callers set its deadline from the item's timeout
// and turn the result into a *TimeoutError with Timed.
type ScriptExecutor interface {
	RunScript(ctx context.Context, scriptPath string, out io.Writer) error
}

// LocalExecutor runs a script on the host with bash. This is the same
// behavior every script execution had before the qemu runtime existed.
//
// Kubeconfig, when set, is exported as KUBECONFIG — a kind lab's isolated
// kubeconfig, so a script's plain `kubectl` hits the lab's cluster rather
// than whatever the user's own current-context points at.
type LocalExecutor struct {
	Kubeconfig string
	// ExtraEnv is added to the script's environment — a linked cluster's
	// ASTRONA_CLUSTER_* variables.
	ExtraEnv []string
}

func (e LocalExecutor) RunScript(ctx context.Context, scriptPath string, out io.Writer) error {
	cmd := Command(ctx, "bash", scriptPath)
	cmd.Env = Env(e.Kubeconfig, e.ExtraEnv)
	cmd.Stdout = out
	cmd.Stderr = out
	return Run(cmd)
}

// KubeconfigEnv is the current environment with KUBECONFIG set to
// kubeconfig, or nil (inherit unchanged, exec.Cmd's default) when
// kubeconfig is "".
func KubeconfigEnv(kubeconfig string) []string {
	if kubeconfig == "" {
		return nil
	}
	return append(os.Environ(), "KUBECONFIG="+kubeconfig)
}

// Env is KubeconfigEnv plus extra variables; nil (inherit unchanged) when
// there's nothing to add.
func Env(kubeconfig string, extra []string) []string {
	env := KubeconfigEnv(kubeconfig)
	if len(extra) == 0 {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	return append(env, extra...)
}

// SSHExecutor runs a script inside a qemu VM over SSH. The script's
// contents are piped over stdin to `bash -s` on the remote side rather than
// interpolated into a remote command string, so script content can never
// break out of argv quoting.
type SSHExecutor struct {
	Host       string
	Port       int
	User       string
	KeyPath    string
	KnownHosts string
}

// On timeout the local ssh is killed, which closes the session: the remote
// shell gets SIGHUP, and the script's children inside the VM go with it
// unless they ignore that.
func (s SSHExecutor) RunScript(ctx context.Context, scriptPath string, out io.Writer) error {
	f, err := os.Open(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to open script '%s': %w", scriptPath, err)
	}
	defer f.Close()

	// Same isolation as the student terminal: no user ssh config, agent,
	// X11 or port forwarding, local commands or escape character.
	args := append(sshopts.Unattended(10),
		"-i", s.KeyPath,
		"-p", fmt.Sprintf("%d", s.Port),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile="+s.KnownHosts,
		"--", fmt.Sprintf("%s@%s", s.User, s.Host),
		"bash", "-s",
	)

	cmd := Command(ctx, "ssh", args...)
	cmd.Stdin = f
	cmd.Stdout = out
	cmd.Stderr = out
	err = Run(cmd)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == sshFailureExit {
		return &SSHConnectError{Host: s.Host, Port: s.Port}
	}
	return err
}

// sshFailureExit is the status ssh exits with when it couldn't connect or
// the connection broke. It can't be told apart from a remote script that
// itself exits 255, so scripts run over SSH must not use 255.
const sshFailureExit = 255

// SSHConnectError means ssh itself failed (exit 255: unreachable VM,
// refused key, dropped connection), so the script's outcome is unknown.
// It is deliberately not an *exec.ExitError: callers that treat a non-zero
// exit as "the script said no" must not count this as one.
type SSHConnectError struct {
	Host string
	Port int
}

func (e *SSHConnectError) Error() string {
	return fmt.Sprintf("ssh to %s:%d exited 255 — the VM is unreachable or the connection failed, so the script's result is unknown", e.Host, e.Port)
}

// ErrNoKubectl is what every command says when kubectl is missing.
var ErrNoKubectl = errors.New("kubectl isn't installed (or isn't on your PATH) — install it: https://kubernetes.io/docs/tasks/tools/ — then run `astrona check`")

// LookKubectl finds kubectl, or returns ErrNoKubectl.
func LookKubectl() (string, error) {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return "", ErrNoKubectl
	}
	return path, nil
}
