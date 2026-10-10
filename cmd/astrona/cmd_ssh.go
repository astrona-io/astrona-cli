package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"astrona/internal/config"
	"astrona/internal/hypervisor"

	"github.com/mattn/go-isatty"
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
	var userFlag string
	var passwordFlag string
	var askPassword bool

	cmd := &cobra.Command{
		Use:               "ssh <lab-name>",
		ValidArgsFunction: labCompletion(isQEMU),
		Short:             "SSH into a running qemu lab's VM",
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// ssh prompts on the terminal; without one it would just fail
			// authentication — say why up front.
			if askPassword && !isatty.IsTerminal(os.Stdin.Fd()) {
				return fmt.Errorf("--ask-password needs an interactive terminal to type the password into")
			}

			// Accept the lab name with or without the "astro-" prefix —
			// `astrona list` prints the prefixed form, but a user typing
			// the bare lab name should still connect.
			name := config.NormalizeClusterName(args[0])

			handle, err := hypervisor.LoadQEMUHandle(name)
			if err != nil {
				return err
			}

			sshPath, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH: %w", err)
			}

			user := handle.SSHUser
			if userFlag != "" {
				user = userFlag
			}

			sshArgs := []string{
				"-p", strconv.Itoa(handle.SSHPort),
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "UserKnownHostsFile=" + handle.KnownHosts,
			}

			if passwordFlag != "" || askPassword {
				sshArgs = append(sshArgs,
					"-o", "PubkeyAuthentication=no",
					"-o", "IdentitiesOnly=yes",
					"-o", "PreferredAuthentications=password,keyboard-interactive",
				)
			} else {
				sshArgs = append(sshArgs, "-i", handle.SSHKeyPath)
			}

			sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", user, handle.SSHHost))

			fmt.Printf("Connecting to '%s' (%s@%s:%d)...\n", handle.ClusterName, user, handle.SSHHost, handle.SSHPort)

			sshCmd := exec.Command(sshPath, sshArgs...)
			sshCmd.Stdin = os.Stdin
			sshCmd.Stdout = os.Stdout
			sshCmd.Stderr = os.Stderr

			// Deprecated --password: ssh has no way to take a password
			// non-interactively, so astrona answers its SSH_ASKPASS
			// prompt itself (main re-enters as the askpass helper). The
			// secret is set only on this ssh process's environment —
			// never astrona's own, and ssh doesn't forward ASTRONA_* to
			// the VM — but ssh's local children (the askpass helper, a
			// ProxyCommand) inherit it, and it is already in argv and
			// shell history. --ask-password avoids all of that: ssh
			// prompts itself, with no echo, and astrona never sees it.
			if passwordFlag != "" {
				executablePath, err := os.Executable()
				if err != nil {
					return fmt.Errorf("failed to get executable path: %w", err)
				}
				env := os.Environ()
				env = append(env, "SSH_ASKPASS="+executablePath)
				env = append(env, "SSH_ASKPASS_REQUIRE=force")
				env = append(env, "ASTRONA_INTERNAL_ASKPASS="+passwordFlag)
				env = append(env, "DISPLAY=dummy:0")
				sshCmd.Env = env
			}

			return sshCmd.Run()
		},
	}

	cmd.Flags().StringVar(&userFlag, "user", "", "SSH username override")
	cmd.Flags().BoolVar(&askPassword, "ask-password", false, "Log in with a password instead of the lab's key: ssh prompts for it (no echo); local SSH keys aren't tried")
	cmd.Flags().StringVar(&passwordFlag, "password", "", "SSH password for authentication (deprecated: visible in ps and shell history — use --ask-password)")
	_ = cmd.Flags().MarkDeprecated("password", "it puts the password in the process list and your shell history; use --ask-password, where ssh prompts for it without echo")
	cmd.MarkFlagsMutuallyExclusive("password", "ask-password")

	return cmd
}
