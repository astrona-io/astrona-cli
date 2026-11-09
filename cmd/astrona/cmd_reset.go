package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/ui"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// confirmReset asks on in/out whether to throw lab away.
func confirmReset(in io.Reader, out io.Writer, lab string) bool {
	return confirmYes(in, out, fmt.Sprintf("Reset %s? This deletes the lab and everything done in it, then recreates it from the config.", lab))
}

// promptOut is where interactive questions are written: stderr, so stdout
// carries only a command's output (exactly one document under -o json).
var promptOut io.Writer = os.Stderr

// confirmYes asks question on in/out; only "y"/"yes" (any case) confirms.
func confirmYes(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func newResetCmd(flags *rootFlags) *cobra.Command {
	var yes, soft, withoutLogin bool
	var clusterFlag string

	cmd := &cobra.Command{
		Use:   "reset [lab]",
		Short: "Start a lab over: destroy it and recreate it from its config",
		Long: "Start a lab over from scratch: run its teardown scripts, destroy it, then do everything " +
			"`astrona run` does — the lab ends up exactly as a fresh `astrona run` leaves it. " +
			"If the lab isn't running, it's simply created.\n\n" +
			"The config is validated before anything is destroyed, so a broken config never leaves " +
			"you without a lab. teardown.keepCluster is ignored — reset always recreates.\n\n" +
			"Asks for confirmation (everything done in the lab is lost); --yes skips it and is " +
			"required with --headless or when not running in a terminal. --headless also skips the login reminder and new account sessions.\n\n" +
			"--cluster <name> rebuilds only that linked cluster (runtime.kind.clusters) of a running lab: " +
			"its teardown scripts, destroy, then create and bootstrap it again (with the addresses of " +
			"the clusters it dependsOn). The lab and its other clusters are left alone; port forwards " +
			"into the rebuilt cluster are restarted.\n\n" +
			"A full reset of a catalog lab asks for confirmation when signed out, like `astrona run` " +
			"(--without-login confirms without prompting). When signed in, " +
			"it starts a fresh session and reports when the rebuilt lab is ready for its course page. --soft and " +
			"--cluster keep the current session and need no sign-in.\n\n" +
			"Like `astrona run`, a full reset points kubectl at the lab (the context to go back to " +
			"is still the one from before the lab first ran); --keep-context leaves it alone.\n\n" +
			"--soft keeps the clusters: it deletes every namespace created after the platform " +
			"(kube-system, addons, …) was set up, clears what isn't astrona's from the default " +
			"namespace, and runs the bootstrap again — seconds instead of minutes. Cluster-wide " +
			"objects (CRDs, ClusterRoles, …) aren't reverted; use a full reset for those. With " +
			"--cluster, only that linked cluster.",
		Example: `  astrona reset -c ./labs/my-lab
  astrona reset -c ./labs/my-lab --yes
  astrona reset -c ./labs/my-lab --cluster idp
  astrona reset -c ./labs/my-lab --soft`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := labArg(args, flags); err != nil { // a lab given as the argument wins over `astrona use`
				return err
			}
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			if err := requireTrust(flags, cfg, baseDir); err != nil {
				return err
			}

			if err := lifecycle.Validate(cfg); err != nil {
				return fmt.Errorf("lab config is invalid, nothing was reset: %w", err)
			}
			if err := validateParallel(flags.parallel); err != nil {
				return err
			}

			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
			release, err := labstate.LockMutation(clusterName)
			if err != nil {
				return err
			}
			defer release()
			if soft {
				what := clusterName
				if clusterFlag != "" {
					what = "linked cluster " + clusterFlag + " of " + clusterName
				}
				if !yes {
					if flags.headless || !isatty.IsTerminal(os.Stdin.Fd()) {
						return fmt.Errorf("refusing to reset '%s' without confirmation — pass --yes in headless mode or when not running in a terminal", clusterName)
					}
					if !confirmYes(os.Stdin, promptOut, fmt.Sprintf("Soft-reset %s? Its lab namespaces and what's in default are deleted, then its bootstrap runs again.", what)) {
						fmt.Println("Reset cancelled — nothing was changed.")
						return nil
					}
				}
				rep, err := ui.New("reset", cfg.Metadata.Name, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
				if err != nil {
					return err
				}
				defer rep.Close()
				forwards, err := labops.SoftReset(cfg, baseDir, clusterName, clusterFlag, rep)
				if err != nil {
					return err
				}
				rep.Close()
				fmt.Printf("\n%s is back to its just-started state (clusters kept, bootstrap re-run).\n", what)
				printPortForwardHints(os.Stdout, forwards)
				return nil
			}
			if clusterFlag != "" {
				return resetLinkedCluster(cfg, baseDir, clusterName, clusterFlag, yes, flags)
			}
			// A signed-in catalog reset starts a fresh session, like run.
			// Check the optional account before anything is destroyed.
			ctx := context.Background()
			acct, err := accountForLab(ctx, flags)
			if err != nil {
				return err
			}
			acct, approved, err := confirmGuestLabRun(cmd, flags, acct, withoutLogin)
			if err != nil {
				return err
			}
			if !approved {
				fmt.Println("Cancelled — sign in with `astrona login` to track your progress, then try again.")
				return nil
			}

			exists, err := labops.LabExists(cfg, clusterName)
			if err != nil {
				return err
			}

			if exists && !yes {
				if flags.headless || !isatty.IsTerminal(os.Stdin.Fd()) {
					return fmt.Errorf("refusing to reset '%s' without confirmation — pass --yes in headless mode or when not running in a terminal", clusterName)
				}
				if !confirmReset(os.Stdin, promptOut, clusterName) {
					fmt.Println("Reset cancelled — nothing was changed.")
					return nil
				}
			}
			start, err := startLabSession(ctx, acct, "reset", cfg, flags.catalogLab)
			if err != nil {
				return err
			}
			defer start.abandon()

			rep, err := ui.New("reset", cfg.Metadata.Name, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()

			endOldSession(clusterName) // an old playground's watchdog would remove the new lab
			if exists {
				// With --keep-context reset won't switch again: the old lab's context goes back first.
				if err := labops.RemoveForRestart(os.Stdout, cfg, baseDir, clusterName, flags.keepContext, false, rep); err != nil {
					return fmt.Errorf("could not remove the old lab, nothing was recreated: %w", err)
				}
			} else {
				rep.Info("Lab '%s' isn't running — creating it fresh.", clusterName)
			}

			if err := bringUpLab(cfg, baseDir, flags, rep, start.sess); err != nil {
				return err
			}
			start.done(clusterName)
			return nil
		},
	}

	cmd.Flags().BoolVar(&withoutLogin, "without-login", false, "Confirm running a catalog lab without signing in or tracking progress on Astrona")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation before deleting and recreating the lab")
	addParallelFlag(cmd, flags)
	addKeepContextFlag(cmd, flags)
	cmd.Flags().StringVar(&clusterFlag, "cluster", "", "Rebuild only this linked cluster (its runtime.kind.clusters name)")
	_ = cmd.RegisterFlagCompletionFunc("cluster", clusterFlagCompletion(flags, false))
	cmd.Flags().BoolVar(&soft, "soft", false, "Keep the cluster(s): delete the lab's namespaces and re-run its bootstrap (seconds instead of minutes)")
	return cmd
}

// resetLinkedCluster rebuilds one linked cluster of the running lab
// clusterName (labops.PlanLinkedReset, then labops.ResetLinkedCluster)
// once the user confirmed, and prints the restarted port forwards.
func resetLinkedCluster(cfg *config.LabConfig, baseDir, clusterName, name string, yes bool, flags *rootFlags) error {
	plan, err := labops.PlanLinkedReset(cfg, clusterName, name)
	if err != nil {
		return err
	}
	target := plan.Target

	if !yes {
		if flags.headless || !isatty.IsTerminal(os.Stdin.Fd()) {
			return fmt.Errorf("refusing to reset '%s' without confirmation — pass --yes in headless mode or when not running in a terminal", target)
		}
		if !confirmYes(os.Stdin, promptOut, fmt.Sprintf("Reset linked cluster %s? Everything done in it is lost; it's recreated from the config.", target)) {
			fmt.Println("Reset cancelled — nothing was changed.")
			return nil
		}
	}

	rep, err := ui.New("reset", cfg.Metadata.Name, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
	if err != nil {
		return err
	}
	defer rep.Close()

	forwards, err := labops.ResetLinkedCluster(cfg, baseDir, plan, rep)
	if err != nil {
		return err
	}

	rep.Close()
	fmt.Printf("\nLinked cluster %s rebuilt from the config — lab %s and its other clusters were left as they were.\n", target, clusterName)
	printPortForwardHints(os.Stdout, forwards)
	return nil
}
