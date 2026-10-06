package executor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
type ScriptExecutor interface {
	RunScript(scriptPath string, out io.Writer) error
}

// LocalExecutor runs a script on the host with bash. This is the same
// behavior every script execution had before the qemu runtime existed.
//
// Kubeconfig, when set, is exported as KUBECONFIG — a kind lab's isolated
// kubeconfig, so a script's plain `kubectl` hits the lab's cluster rather
// than whatever the user's own current-context points at.
type LocalExecutor struct {
	Kubeconfig string
	// ExtraEnv is added to the script's environment — a linked lab's
	// ASTRONA_LINK_* variables.
	ExtraEnv []string
}

func (e LocalExecutor) RunScript(scriptPath string, out io.Writer) error {
	cmd := exec.Command("bash", scriptPath)
	cmd.Env = Env(e.Kubeconfig, e.ExtraEnv)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
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

func (s SSHExecutor) RunScript(scriptPath string, out io.Writer) error {
	f, err := os.Open(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to open script '%s': %w", scriptPath, err)
	}
	defer f.Close()

	args := []string{
		"-i", s.KeyPath,
		"-p", fmt.Sprintf("%d", s.Port),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + s.KnownHosts,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		fmt.Sprintf("%s@%s", s.User, s.Host),
		"bash", "-s",
	}

	cmd := exec.Command("ssh", args...)
	cmd.Stdin = f
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
