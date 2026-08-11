package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/spf13/cobra"
)

// newSSHCmd builds `astrona ssh <lab-name>`: opens an interactive SSH
// session into a running qemu lab VM. Takes the lab name directly (the same
// name `astrona list` prints) rather than `--config` — LoadQEMUHandle only
// ever needed the name, not the lab config, so requiring a resolvable
// config path just to SSH into an already-running VM was unnecessary
// friction (and broke if that config path/URL/git remote wasn't reachable
// anymore, even though the VM itself was fine). A lab with no matching qemu
// state is never a kind lab pretending to be one: only the qemu runtime
// ever writes to ~/.astrona/qemu, so LoadQEMUHandle succeeding is itself the
// proof this is a qemu lab.
//
// Reuses the same ephemeral key/known_hosts every lab script already runs
// through (SSHExecutor in executor.go) — no new credential, no new trust
// decision, just a human-facing door into what astrona already has access
// to.
func newSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "ssh <lab-name>",
		Short:        "SSH into a running qemu lab VM (name as shown by 'astrona list')",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			handle, err := LoadQEMUHandle(name)
			if err != nil {
				return err
			}

			sshPath, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH: %w", err)
			}

			sshArgs := []string{
				"-i", handle.SSHKeyPath,
				"-p", strconv.Itoa(handle.SSHPort),
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "UserKnownHostsFile=" + handle.KnownHosts,
				fmt.Sprintf("%s@%s", handle.SSHUser, handle.SSHHost),
			}

			fmt.Printf("Connecting to '%s' (%s@%s:%d)...\n", handle.ClusterName, handle.SSHUser, handle.SSHHost, handle.SSHPort)

			sshCmd := exec.Command(sshPath, sshArgs...)
			sshCmd.Stdin = os.Stdin
			sshCmd.Stdout = os.Stdout
			sshCmd.Stderr = os.Stderr
			return sshCmd.Run()
		},
	}
}
