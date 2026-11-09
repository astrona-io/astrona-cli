package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/lifecycle"

	"github.com/mattn/go-isatty"

	"github.com/spf13/cobra"
)

// resolveKindCluster picks the kind lab a kubeconfig/shell command acts on
// (labops.ResolveKindCluster), loading the lab config from -c/--file/--git
// only when no lab-name was given.
func resolveKindCluster(labArg string, flags *rootFlags) (string, error) {
	return labops.ResolveKindCluster(labArg, func() (*config.LabConfig, func(), error) {
		cfg, _, cleanup, err := LoadLabForCommand(flags)
		return cfg, cleanup, err
	})
}

func newKubeconfigCmd(flags *rootFlags) *cobra.Command {
	var clusterFlag string
	cmd := &cobra.Command{
		Use:               "kubeconfig [lab-name]",
		ValidArgsFunction: labCompletion(isKind),
		Short:             "Print the path of a kind lab's own kubeconfig",
		Long: "Print the path of a kind lab's isolated kubeconfig (~/" + apphome.Name() + "/kind/<lab>/kubeconfig), " +
			"which contains only that lab's cluster.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab.",
		Example: `  export KUBECONFIG=$(astrona kubeconfig my-lab)
  kubectl --kubeconfig "$(astrona kubeconfig)" get pods -A
  kubectl --kubeconfig "$(astrona kubeconfig my-lab --cluster idp)" get pods -A`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			labName, err := catalogLabArg(firstArg(args), flags)
			if err != nil {
				return err
			}
			lab, err := resolveKindCluster(labName, flags)
			if err != nil {
				return err
			}
			if clusterFlag != "" {
				links, _ := lifecycle.Links(lab)
				if lab, err = lifecycle.FindLinkedCluster(lab, clusterFlag, links); err != nil {
					return err
				}
			}
			path, err := labops.LabKubeconfig(lab)
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	}
	cmd.Flags().StringVar(&clusterFlag, "cluster", "", "A linked cluster's kubeconfig instead (its runtime.kind.clusters name)")
	_ = cmd.RegisterFlagCompletionFunc("cluster", clusterFlagCompletion(flags, true))
	return cmd
}

func newShellCmd(flags *rootFlags) *cobra.Command {
	var clusterFlag string
	cmd := &cobra.Command{
		Use:               "shell [lab-name] [-- command [args...]]",
		ValidArgsFunction: labCompletion(isKind),
		Short:             "Open an isolated terminal (or run one command) for a Kind lab",
		Long: "Open an isolated container terminal with this Kind lab's credentials, so plain " +
			"`kubectl`, `helm`, `k9s`, … talk to the lab — without changing your own kubectl " +
			"current-context. Files are saved under ~/" + apphome.Name() + "/runtime/<lab>/work, mounted as /workspace.\nYour home directory and host tools are not mounted. Tools are downloaded on first use.\nType `exit` to leave. $" + labops.LabShellEnvVar + " holds the lab name inside " +
			"the shell (add it to your prompt if you like).\n\n" +
			"After `--`, runs that one command instead of a shell (no shell parsing).\n\n" +
			"A lab with linked clusters (runtime.kind.clusters) gets all of them: `kubectl --context " +
			"$ASTRONA_CLUSTER_<NAME>_CONTEXT` reaches one. --cluster <name> makes a linked cluster the " +
			"default instead.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab. " +
			"For qemu labs, use `astrona ssh`.",
		Example: `  astrona shell my-lab
  astrona shell -- kubectl get pods -A
  astrona shell my-lab -- k9s
  astrona shell my-lab --cluster idp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			labArgs, command := args, []string(nil)
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				labArgs, command = args[:dash], args[dash:]
			}
			if len(labArgs) > 1 {
				return fmt.Errorf("expected at most one lab-name, got %d (put a command after --)", len(labArgs))
			}
			if len(command) == 0 {
				if cur := os.Getenv(labops.LabShellEnvVar); cur != "" {
					return fmt.Errorf("already inside an astrona shell for '%s' — `exit` first", cur)
				}
			}

			labName, err := catalogLabArg(firstArg(labArgs), flags)
			if err != nil {
				return err
			}
			lab, err := resolveKindCluster(labName, flags)
			if err != nil {
				return err
			}
			return runLabShell(lab, clusterFlag, command)
		},
	}
	cmd.Flags().StringVar(&clusterFlag, "cluster", "", "Make a linked cluster (its runtime.kind.clusters name) the default kubectl context")
	_ = cmd.RegisterFlagCompletionFunc("cluster", clusterFlagCompletion(flags, true))
	return cmd
}

// runLabShell opens the isolated lab terminal attached to this one.
func runLabShell(lab, clusterFlag string, command []string) error {
	c, target, err := labShellCommand(lab, clusterFlag, command)
	if err != nil {
		return err
	}

	if c.Cancel != nil {
		defer func() { _ = c.Cancel() }()
	}

	if len(command) > 0 {
		return runAttached(c)
	}

	fmt.Fprintf(os.Stderr, "Opening isolated terminal for %s (context kind-%s). Work in /workspace; type `exit` to leave.\n", lab, target)
	err = runAttached(c)
	fmt.Fprintf(os.Stderr, "Left lab shell for %s.\n", lab)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil // the shell's own exit status is the user's last command, not ours
	}
	return err
}

// labShellCommand is the isolated lab terminal, shared with embedded
// terminals (labops.ShellCommand). The container gets a terminal for a
// shell, and for a command when stdin is one.
func labShellCommand(lab, clusterFlag string, command []string) (*exec.Cmd, string, error) {
	return labShellCommandContext(context.Background(), lab, clusterFlag, command)
}

func labShellCommandContext(ctx context.Context, lab, clusterFlag string, command []string) (*exec.Cmd, string, error) {
	return labops.ShellCommand(ctx, lab, clusterFlag, command, len(command) == 0 || isatty.IsTerminal(os.Stdin.Fd()))
}

// Catch Ctrl-C in Astrona while the child handles it. Ignoring SIGINT would
// pass SIG_IGN into the shell and make its commands ignore Ctrl-C as well.
func runAttached(c *exec.Cmd) error {
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
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
