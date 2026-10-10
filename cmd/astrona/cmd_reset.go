package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// labExists reports whether any part of cfg's environment exists under
// clusterName: the kind cluster, the single qemu VM, or any VM of a
// multi-VM qemu lab.
func labExists(cfg *config.LabConfig, clusterName string) bool {
	if cfg.Runtime.Type == string(runtime.RuntimeQEMU) {
		if qemuStateExists(clusterName) {
			return true
		}
		for _, vm := range cfg.Runtime.QEMU {
			if vm.Name != "" && qemuStateExists(clusterName+"-"+vm.Name) {
				return true
			}
		}
		return false
	}
	return kindClusterExists(clusterName)
}

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
	var yes, soft bool
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
			"required when not running in a terminal.\n\n" +
			"--cluster <name> rebuilds only that linked cluster (runtime.kind.clusters) of a running lab: " +
			"its teardown scripts, destroy, then create and bootstrap it again (with the addresses of " +
			"the clusters it dependsOn). The lab and its other clusters are left alone; port forwards " +
			"into the rebuilt cluster are restarted.\n\n" +
			"A full reset of a catalog lab starts a fresh lab session on Astrona, like `astrona run`: " +
			"it needs `astrona login`, and the new lab page opens once the lab is ready. --soft and " +
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
			if soft {
				what := clusterName
				if clusterFlag != "" {
					what = "linked cluster " + clusterFlag + " of " + clusterName
				}
				if !yes {
					if !isatty.IsTerminal(os.Stdin.Fd()) {
						return fmt.Errorf("refusing to reset '%s' without confirmation — pass --yes when not running in a terminal", clusterName)
					}
					if !confirmYes(os.Stdin, promptOut, fmt.Sprintf("Soft-reset %s? Its lab namespaces and what's in default are deleted, then its bootstrap runs again.", what)) {
						fmt.Println("Reset cancelled — nothing was changed.")
						return nil
					}
				}
				rep, err := ui.NewReporter("reset", cfg.Metadata.Name, flags.verbose)
				if err != nil {
					return err
				}
				defer rep.Close()
				if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil { // the lab's existing CA
					return err
				}
				forwards, err := lifecycle.SoftReset(cfg, baseDir, clusterName, clusterFlag, rep)
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
			// A full reset of a catalog lab starts a fresh lab session, like
			// `astrona run`: checked before anything is destroyed.
			ctx := context.Background()
			acct, err := requireSignIn(ctx, flags, "reset")
			if err != nil {
				return err
			}
			exists := labExists(cfg, clusterName)

			if exists && !yes {
				if !isatty.IsTerminal(os.Stdin.Fd()) {
					return fmt.Errorf("refusing to reset '%s' without confirmation — pass --yes when not running in a terminal", clusterName)
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

			rep, err := ui.NewReporter("reset", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			endOldSession(clusterName) // an old playground's watchdog would remove the new lab
			if exists {
				if flags.keepContext { // not switching again: put back what the old lab switched away from
					releaseLab(os.Stdout, clusterName)
				}
				teardown := cfg.Teardown
				teardown.KeepCluster = false
				info := teardownInfo{clusterName: clusterName, teardown: teardown, runtime: cfg.Runtime}
				if err := tearDownLabEnvironment(clusterName, info, baseDir, true, rep); err != nil {
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

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Don't ask for confirmation")
	addParallelFlag(cmd, flags)
	addKeepContextFlag(cmd, flags)
	cmd.Flags().StringVar(&clusterFlag, "cluster", "", "Rebuild only this linked cluster (its runtime.kind.clusters name)")
	_ = cmd.RegisterFlagCompletionFunc("cluster", clusterFlagCompletion(flags, false))
	cmd.Flags().BoolVar(&soft, "soft", false, "Keep the cluster(s): delete the lab's namespaces and re-run its bootstrap (seconds instead of minutes)")
	return cmd
}

// resetLinkedCluster rebuilds one linked cluster of the running lab
// clusterName: teardown scripts, destroy, then create + bootstrap it again
// from the config, with its dependencies' addresses. Port forwards into it
// are restarted (their supervisors give up when the cluster goes away).
func resetLinkedCluster(cfg *config.LabConfig, baseDir, clusterName, name string, yes bool, flags *rootFlags) error {
	order, states, err := lifecycle.ClusterStates(cfg, clusterName)
	if err != nil {
		return err
	}
	idx := -1
	for i, l := range order {
		if l.Name == name {
			idx = i
		}
	}
	if idx < 0 {
		_, err := lifecycle.FindLinkedCluster(clusterName, name, states)
		return err
	}
	if !kindClusterExists(clusterName) {
		return fmt.Errorf("lab %s isn't running — `astrona run` creates it with all its clusters", clusterName)
	}
	l, target := order[idx], states[idx].Cluster

	if !yes {
		if !isatty.IsTerminal(os.Stdin.Fd()) {
			return fmt.Errorf("refusing to reset '%s' without confirmation — pass --yes when not running in a terminal", target)
		}
		if !confirmYes(os.Stdin, promptOut, fmt.Sprintf("Reset linked cluster %s? Everything done in it is lost; it's recreated from the config.", target)) {
			fmt.Println("Reset cancelled — nothing was changed.")
			return nil
		}
	}

	rep, err := ui.NewReporter("reset", cfg.Metadata.Name, flags.verbose)
	if err != nil {
		return err
	}
	defer rep.Close()

	lifecycle.RunLinkedTeardown([]config.KindCluster{l}, clusterName, baseDir, rep)
	if kindClusterExists(target) {
		if err := lifecycle.DestroyKindCluster(target, rep); err != nil {
			return fmt.Errorf("could not remove linked cluster %s, nothing was recreated: %w", target, err)
		}
	}

	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil { // the lab's existing CA
		return err
	}
	byName := map[string]cluster.LinkState{}
	for _, s := range states {
		byName[s.Name] = s
	}
	var deps []cluster.LinkState
	for _, d := range l.DependsOn {
		deps = append(deps, byName[d])
	}
	rep.Section("Linked cluster '%s'", l.Name)
	if _, _, err := lifecycle.Up(lifecycle.LinkedClusterConfig(cfg, l), baseDir, target, deps, false, rep); err != nil {
		return fmt.Errorf("linked cluster '%s' (%s): %w", l.Name, target, err)
	}
	if err := lifecycle.ApplyWAN(target, l.WAN, rep); err != nil {
		return fmt.Errorf("linked cluster '%s' (%s): %w", l.Name, target, err)
	}
	// Its node has a new IP; every cluster of the lab needs to know it.
	lifecycle.RefreshLinkNames(clusterName, false, rep)

	var forwards []portforward.Forward
	for _, pf := range cfg.Runtime.PortForwards {
		if pf.Cluster == l.Name {
			rep.Section("Port forwards")
			forwards, err = lifecycle.StartPortForwards(clusterName, cfg.Runtime.PortForwards, rep)
			if err != nil {
				rep.Warn("some port forwards could not be restarted — `astrona port-forward start -c <config>`")
			}
			break
		}
	}

	rep.Close()
	fmt.Printf("\nLinked cluster %s rebuilt from the config — lab %s and its other clusters were left as they were.\n", target, clusterName)
	printPortForwardHints(os.Stdout, forwards)
	return nil
}
