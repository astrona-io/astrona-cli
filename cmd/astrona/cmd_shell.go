package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// labShellEnvVar is set inside `astrona shell` — guards against nesting
// and lets a user show the lab in their prompt.
const labShellEnvVar = "ASTRONA_LAB"

// resolveKindLab picks the kind lab a kubeconfig/shell command acts on, in
// order: an explicit lab-name argument; the lab config resolved from
// -c/--file/--git (if it loads and is a kind lab); otherwise the only
// running astrona kind lab. Refuses to guess between several.
func resolveKindLab(labArg string, flags *rootFlags) (string, error) {
	if labArg != "" {
		name := config.NormalizeClusterName(labArg)
		if !kindClusterExists(name) {
			return "", fmt.Errorf("no running kind lab named '%s' — run `astrona list` to see what's running", name)
		}
		return name, nil
	}

	if cfg, _, cleanup, err := LoadLabForCommand(flags); err == nil {
		defer cleanup()
		if cfg.Runtime.Type == "" || cfg.Runtime.Type == string(runtime.RuntimeKind) {
			name := config.NormalizeClusterName(cfg.Metadata.Name)
			if kindClusterExists(name) {
				return name, nil
			}
			return "", fmt.Errorf("lab '%s' from the config isn't running — start it with `astrona run`", name)
		}
		return "", fmt.Errorf("lab '%s' uses the %s runtime — use `astrona ssh` instead", cfg.Metadata.Name, cfg.Runtime.Type)
	}

	var kindLabs []string
	for _, r := range collectKindRows() {
		if !strings.HasPrefix(r.name, "astro-test-") {
			kindLabs = append(kindLabs, r.name)
		}
	}
	switch len(kindLabs) {
	case 0:
		return "", fmt.Errorf("no astrona kind lab is running — start one with `astrona run`")
	case 1:
		return kindLabs[0], nil
	default:
		return "", fmt.Errorf("several kind labs are running — pick one: %s", strings.Join(kindLabs, ", "))
	}
}

// labKubeconfig returns lab's isolated kubeconfig, (re)writing it first if
// it's missing — e.g. the lab was created by an astrona version from
// before kubeconfig isolation.
func labKubeconfig(lab string) (string, error) {
	if path := cluster.ExistingKubeconfig(lab); path != "" {
		return path, nil
	}
	return cluster.WriteLabKubeconfig(lab, ui.Discard())
}

func newKubeconfigCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "kubeconfig [lab-name]",
		Short: "Print the path of a kind lab's own kubeconfig",
		Long: "Print the path of a kind lab's isolated kubeconfig (~/.astrona/kind/<lab>/kubeconfig), " +
			"which contains only that lab's cluster.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab.",
		Example: `  export KUBECONFIG=$(astrona kubeconfig my-lab)
  kubectl --kubeconfig "$(astrona kubeconfig)" get pods -A`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := resolveKindLab(firstArg(args), flags)
			if err != nil {
				return err
			}
			path, err := labKubeconfig(lab)
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	}
}

func newShellCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "shell [lab-name] [-- command [args...]]",
		Short: "Open a shell (or run one command) with kubectl pointed at a kind lab",
		Long: "Open your $SHELL with KUBECONFIG set to a kind lab's isolated kubeconfig, so plain " +
			"`kubectl`, `helm`, `k9s`, … talk to the lab — without changing your own kubectl " +
			"current-context. Type `exit` to leave. $" + labShellEnvVar + " holds the lab name inside " +
			"the shell (add it to your prompt if you like).\n\n" +
			"After `--`, runs that one command instead of a shell (no shell parsing).\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab. " +
			"For qemu labs, use `astrona ssh`.",
		Example: `  astrona shell my-lab
  astrona shell -- kubectl get pods -A
  astrona shell my-lab -- k9s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			labArgs, command := args, []string(nil)
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				labArgs, command = args[:dash], args[dash:]
			}
			if len(labArgs) > 1 {
				return fmt.Errorf("expected at most one lab-name, got %d (put a command after --)", len(labArgs))
			}
			if len(command) == 0 {
				if cur := os.Getenv(labShellEnvVar); cur != "" {
					return fmt.Errorf("already inside an astrona shell for '%s' — `exit` first", cur)
				}
			}

			lab, err := resolveKindLab(firstArg(labArgs), flags)
			if err != nil {
				return err
			}
			kubeconfig, err := labKubeconfig(lab)
			if err != nil {
				return err
			}

			env := append(os.Environ(), "KUBECONFIG="+kubeconfig, labShellEnvVar+"="+lab)

			if len(command) > 0 {
				c := exec.Command(command[0], command[1:]...)
				c.Env = env
				return runAttached(c)
			}

			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "/bin/sh"
			}
			fmt.Fprintf(os.Stderr, "Entering lab shell for %s — kubectl now targets context kind-%s. Type `exit` to leave.\n", lab, lab)
			c := exec.Command(shell)
			c.Env = env
			err = runAttached(c)
			fmt.Fprintf(os.Stderr, "Left lab shell for %s.\n", lab)
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return nil // the shell's own exit status is the user's last command, not ours
			}
			return err
		},
	}
}

// runAttached runs c on this terminal. Ctrl-C is meant for the child (it
// shares our process group), so astrona ignores SIGINT while it runs
// rather than dying and orphaning it.
func runAttached(c *exec.Cmd) error {
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s: %w", c.Args[0], err)
	}
	return nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
