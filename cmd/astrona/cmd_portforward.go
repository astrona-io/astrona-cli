package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"astrona/internal/config"
	"astrona/internal/portforward"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// portForwardReadyTimeout is how long `run` / `port-forward start` wait for
// every forward to report Ready before printing what they have.
const portForwardReadyTimeout = 30 * time.Second

// newPortForwardCmd builds `astrona port-forward` (alias `pf`): inspect and
// manage the host-side kubectl port-forwards a kind lab declares in
// runtime.portForwards. `astrona run` starts them; `astrona destroy` stops
// them.
func newPortForwardCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "port-forward",
		Aliases: []string{"pf"},
		Short:   "Show and manage a kind lab's port forwards (runtime.portForwards)",
		Long: "Show and manage the port forwards a kind lab declares in runtime.portForwards.\n\n" +
			"`astrona run` starts one background supervisor per forward, which keeps " +
			"`kubectl port-forward` running on 127.0.0.1 and restarts it whenever it exits " +
			"(e.g. the pod behind a service restarted). `astrona destroy` stops them.\n\n" +
			"Status:\n" +
			"  Ready     kubectl is forwarding and the local port accepts connections\n" +
			"  NotReady  not forwarding yet/again (pod not running, no endpoints, restart backoff)\n" +
			"  Error     the same non-transient kubectl failure 3+ times in a row, e.g. service\n" +
			"            not found (still retrying; pod-not-running-yet stays NotReady)\n" +
			"  Stopped   the supervisor is no longer running — `astrona port-forward start` to restart",
	}
	cmd.AddCommand(
		newPortForwardListCmd(),
		newPortForwardStartCmd(flags),
		newPortForwardStopCmd(flags),
		newPortForwardSuperviseCmd(),
	)
	return cmd
}

// --- port-forward list -----------------------------------------------------

func newPortForwardListCmd() *cobra.Command {
	var output string
	var watch bool

	cmd := &cobra.Command{
		Use:               "list [lab-name]",
		ValidArgsFunction: labCompletion(isKind),
		Aliases:           []string{"ls"},
		Short:             "List port forwards and their status (all labs, or one)",
		Example: `  astrona port-forward list
  astrona pf list my-lab -o wide
  astrona pf list --watch`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "" && output != "wide" {
				return fmt.Errorf("unsupported --output '%s' (only 'wide')", output)
			}
			lab := ""
			if len(args) == 1 {
				lab = config.NormalizeClusterName(args[0])
			}
			wide := output == "wide"

			if !watch {
				fs, err := portforward.List(lab)
				if err != nil {
					return err
				}
				printPortForwardTable(os.Stdout, fs, wide)
				return nil
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				fs, err := portforward.List(lab)
				if err != nil {
					return err
				}
				fmt.Print("\033[H\033[2J")
				fmt.Printf("Every 2s: astrona port-forward list (Ctrl-C to quit)    %s\n\n", time.Now().Format(time.TimeOnly))
				printPortForwardTable(os.Stdout, fs, wide)
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
			}
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format: 'wide' adds namespace, PID and last error")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh every 2s until Ctrl-C")
	return cmd
}

// printPortForwardTable renders forwards kubectl-get style. Effective()
// does a live liveness/TCP check per row, so this always reflects now, not
// just what the supervisor last wrote.
func printPortForwardTable(w io.Writer, fs []portforward.Forward, wide bool) {
	if len(fs) == 0 {
		fmt.Fprintln(w, "No port forwards found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	header := "NAME\tLAB\tSTATUS\tAGE\tRESTARTS\tLOCAL\tTARGET"
	if wide {
		header += "\tNAMESPACE\tPID\tLAST ERROR"
	}
	fmt.Fprintln(tw, header)

	for _, f := range fs {
		pf := f.Spec.Forward.Normalized()
		age := "-"
		if !f.Spec.StartedAt.IsZero() {
			age = formatUptime(time.Since(f.Spec.StartedAt))
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s\t%s:%d",
			pf.Name, f.Spec.Lab, f.Effective(), age, f.Status.Restarts,
			portforward.LocalURL(pf), pf.Resource, pf.TargetPort)
		if wide {
			lastErr := f.Status.LastError
			if lastErr == "" {
				lastErr = "-"
			}
			row += fmt.Sprintf("\t%s\t%d\t%s", pf.Namespace, f.PID, lastErr)
		}
		fmt.Fprintln(tw, row)
	}
	tw.Flush()
}

// printPortForwardHints is the post-`run`/`start` summary: how to reach
// each forward, its current status, and why a not-ready one isn't ready.
func printPortForwardHints(w io.Writer, fs []portforward.Forward) {
	if len(fs) == 0 {
		return
	}

	fmt.Fprintf(w, "\nPort forwards (bound to %s only):\n", portforward.ListenAddress)
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	var notes []string
	for _, f := range fs {
		pf := f.Spec.Forward.Normalized()
		state := f.Effective()
		fmt.Fprintf(tw, "    %s\t%s\t%s\t->  %s:%d (ns %s)\t%s\n",
			pf.Name, state, portforward.LocalURL(pf), pf.Resource, pf.TargetPort, pf.Namespace, pf.Description)
		if state != portforward.StateReady && f.Status.LastError != "" {
			notes = append(notes, fmt.Sprintf("    %s: %s", pf.Name, f.Status.LastError))
		}
	}
	tw.Flush()

	if len(notes) > 0 {
		fmt.Fprintf(w, "\n  Not ready yet (still retrying in the background):\n%s\n", strings.Join(notes, "\n"))
	}
	fmt.Fprintf(w, "\n  Status:  astrona port-forward list [-o wide] [--watch]\n")
	fmt.Fprintf(w, "  Restart: astrona port-forward start -c <config>    Stop: astrona port-forward stop <lab>\n")
}

func countNotReady(fs []portforward.Forward) int {
	n := 0
	for _, f := range fs {
		if f.Effective() != portforward.StateReady {
			n++
		}
	}
	return n
}

// startLabPortForwards starts cfg's forwards for clusterName and waits for
// them. Shared by `run` and `port-forward start`. Never fails the caller
// for a forward that's merely slow — the supervisor keeps retrying.
func startLabPortForwards(clusterName string, forwards []config.PortForward, rep *ui.Reporter) ([]portforward.Forward, error) {
	startErr := portforward.Start(clusterName, forwards, rep)
	if startErr != nil {
		rep.Info("port forward start errors: %s", startErr)
	}

	t := rep.Step("Wait for port forwards to become ready")
	fs, err := portforward.WaitReady(clusterName, portForwardReadyTimeout)
	if err != nil {
		return nil, t.Fail(err)
	}
	if n := countNotReady(fs); n > 0 {
		t.Skip("%d of %d not ready after %s — still retrying in the background", n, len(fs), portForwardReadyTimeout)
	} else {
		t.Done()
	}
	return fs, startErr
}

// --- port-forward start ----------------------------------------------------

func newPortForwardStartCmd(flags *rootFlags) *cobra.Command {
	var wait bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "(Re)start the port forwards of a running kind lab from its config",
		Long: "(Re)start the port forwards declared in the lab config's runtime.portForwards " +
			"against its already-running kind cluster — e.g. after a reboot or `port-forward stop`. " +
			"Any forwards already running for the lab are replaced.\n\n" +
			"Waits up to 30s for every forward to become Ready. With --wait, a forward that " +
			"isn't Ready by then makes the command exit non-zero.",
		Example: `  astrona port-forward start -c ./labs/my-lab
  astrona pf start -c . --wait`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			if err := config.ValidatePortForwards(cfg.Runtime); err != nil {
				return err
			}
			if len(cfg.Runtime.PortForwards) == 0 {
				return fmt.Errorf("lab '%s' declares no runtime.portForwards", cfg.Metadata.Name)
			}

			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
			if !kindClusterExists(clusterName) {
				return fmt.Errorf("no kind cluster '%s' is running — start the lab with `astrona run` first", clusterName)
			}

			rep, err := ui.NewReporter("port-forward", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			rep.Section("Port forwards: %s", clusterName)
			fs, startErr := startLabPortForwards(clusterName, cfg.Runtime.PortForwards, rep)
			rep.Close()
			printPortForwardHints(os.Stdout, fs)

			if startErr != nil {
				return fmt.Errorf("some port forwards could not be started: %w", startErr)
			}
			if n := countNotReady(fs); wait && n > 0 {
				return fmt.Errorf("%d port forward(s) not ready after %s", n, portForwardReadyTimeout)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&wait, "wait", false, "Exit non-zero if any forward isn't Ready within 30s")
	return cmd
}

// --- port-forward stop -----------------------------------------------------

func newPortForwardStopCmd(flags *rootFlags) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:               "stop [lab-name]",
		ValidArgsFunction: labCompletion(isKind),
		Short:             "Stop a lab's port forwards (the cluster keeps running)",
		Long: "Stop a lab's port forwards. The kind cluster itself keeps running.\n\n" +
			"With a lab-name (as shown by `astrona port-forward list`, with or without the " +
			"'astro-' prefix), stops that lab's forwards. With --all, stops every lab's. " +
			"With neither, uses the lab config resolved from -c/--file/--git.",
		Example: `  astrona port-forward stop my-lab
  astrona pf stop --all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) > 0 {
					return fmt.Errorf("--all and a lab-name are mutually exclusive")
				}
				labs, err := portforward.StopAll()
				if len(labs) == 0 && err == nil {
					fmt.Println("No port forwards running.")
				}
				for _, l := range labs {
					fmt.Printf("Stopped port forwards for '%s'.\n", l)
				}
				return err
			}

			var lab string
			if len(args) == 1 {
				lab = config.NormalizeClusterName(args[0])
			} else {
				cfg, _, configCleanup, err := LoadLabForCommand(flags)
				if err != nil {
					return fmt.Errorf("%w (or pass a lab-name / --all)", err)
				}
				defer configCleanup()
				lab = config.NormalizeClusterName(cfg.Metadata.Name)
			}

			n, err := portforward.Stop(lab)
			if err != nil {
				return err
			}
			if n == 0 {
				fmt.Printf("No port forwards running for '%s'.\n", lab)
				return nil
			}
			fmt.Printf("Stopped %d port forward(s) for '%s'.\n", n, lab)
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Stop the port forwards of every lab")
	return cmd
}

// --- port-forward supervise (internal) -------------------------------------

// newPortForwardSuperviseCmd is the detached background process
// portforward.Start launches, one per forward. Hidden: never run by hand.
// Its stdout/stderr are already redirected to the forward's
// supervisor.log by the parent.
func newPortForwardSuperviseCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "supervise <lab> <name>",
		Short:  "Internal: keep one port forward running (started by `astrona run`)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return portforward.Supervise(ctx, args[0], args[1], os.Stdout)
		},
	}
}
