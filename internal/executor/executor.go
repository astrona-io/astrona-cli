package executor

import (
	"fmt"
	"os"
	"os/exec"
)

// ScriptExecutor runs a script that has already been resolved to a local
// path on disk. RunInitScripts/Proctor.runScript own getting the script
// there (local file or downloaded URL) and stay ignorant of where it
// actually executes: LocalExecutor runs it on the host (the kind runtime),
// SSHExecutor runs it inside a VM (the qemu runtime).
type ScriptExecutor interface {
	RunScript(scriptPath string) error
}

// LocalExecutor runs a script on the host with bash. This is the same
// behavior every script execution had before the qemu runtime existed.
type LocalExecutor struct{}

func (LocalExecutor) RunScript(scriptPath string) error {
	cmd := exec.Command("bash", scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

func (s SSHExecutor) RunScript(scriptPath string) error {
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
