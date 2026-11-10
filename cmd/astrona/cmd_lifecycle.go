package main

import (
	"fmt"
	"os"
	"strings"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// resolveLifecycleLab picks the kind lab `stop`/`start` act on
// (labops.ResolveKindLab): the lab config from -c/--file/--git is loaded
// only when no name is given. A catalog lab name selects its config.
func resolveLifecycleLab(cmd *cobra.Command, labArg string, flags *rootFlags, want func(status string) bool, verb string) (string, error) {
	// A catalog name (ATS016/…/lab-01) acts on that lab through its config.
	labArg, err := catalogLabArg(labArg, flags)
	if err != nil {
		return "", err
	}
	var cfg *config.LabConfig
	if labArg == "" {
		if loaded, _, cleanup, err := LoadLabForCommand(flags); err == nil {
			defer cleanup()
			cfg = loaded
		} else {
			if labSourceSelected(cmd, flags) || labConfigHere(flags) {
				return "", fmt.Errorf("load selected lab: %w", err)
			}
			if err := labDiscoveryAllowed(); err != nil {
				return "", err
			}
		}
	}
	return labops.ResolveKindLab(labArg, cfg, want, verb)
}

func labSourceSelected(cmd *cobra.Command, flags *rootFlags) bool {
	if flags.fromCurrent || flags.gitURL != "" || flags.labArg != "" {
		return true
	}
	for _, name := range []string{"config", "file", "git", "git-ref"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func labDiscoveryAllowed() error {
	saved, err := loadCurrentLab()
	if err != nil {
		return fmt.Errorf("read saved lab selection: %w", err)
	}
	if saved != nil {
		return fmt.Errorf("the saved lab selection could not be loaded; select a lab explicitly")
	}
	return nil
}

func newStopCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:               "stop [lab-name]",
		ValidArgsFunction: labCompletion(func(r labops.LabRow) bool { return isKind(r) && isRunning(r) }),
		Short:             "Pause a kind lab (frees CPU/RAM, keeps everything) — resume with astrona start",
		Long: "Stop a kind lab's node containers and pause its port forwards. Nothing is deleted: " +
			"`astrona start` brings the cluster, its workloads and its port forwards back.\n\n" +
			"The lab's linked clusters (runtime.kind.clusters) are stopped with it.\n\n" +
			"Not supported for labs with more than one control plane — their node IPs change on " +
			"restart, which breaks etcd. qemu labs aren't supported yet.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := resolveLifecycleLab(cmd, firstArg(args), flags, func(s string) bool { return s != "Stopped" }, "stop")
			if err != nil {
				return err
			}

			// Refuses an HA cluster before touching anything else.
			plan, err := labops.PlanStop(lab)
			if err != nil {
				return err
			}

			rep, err := ui.New("stop", lab, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()

			if err := labops.Stop(plan, rep); err != nil {
				return err
			}

			rep.Close()
			fmt.Printf("\nLab %s stopped — its cluster, workloads and port forwards are kept.\n", lab)
			if len(plan.Linked) > 0 {
				fmt.Printf("Linked clusters stopped too: %s.\n", strings.Join(plan.Linked, ", "))
			}
			fmt.Printf("Resume with: astrona start %s\n", lab)
			return nil
		},
	}
}

func newStartCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:               "start [lab-name]",
		ValidArgsFunction: labCompletion(isStoppedKind),
		Short:             "Resume a kind lab paused with astrona stop",
		Long: "Start a stopped kind lab's node containers, wait for its API, and restart its port " +
			"forwards. Its linked clusters (runtime.kind.clusters) are started first — the lab needs them.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only stopped kind lab.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := resolveLifecycleLab(cmd, firstArg(args), flags, func(s string) bool {
				return s == "Stopped" || strings.HasPrefix(s, "Degraded")
			}, "start")
			if err != nil {
				return err
			}

			rep, err := ui.New("start", lab, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()

			res, err := labops.Start(lab, rep)
			if err != nil {
				return err
			}

			rep.Close()
			fmt.Printf("\nLab %s started — %s.\n", lab, res.Health)
			if len(res.Linked) > 0 {
				fmt.Printf("Linked clusters started too: %s.\n", strings.Join(res.Linked, ", "))
			}
			if !strings.HasPrefix(res.Health, "Ready") {
				fmt.Printf("Nodes can take a minute to report Ready after a restart — check `astrona list`.\n")
			}
			fmt.Printf("\nConnect:\n    astrona shell %s\n    kubectl --context kind-%s ...\n", lab, lab)
			printPortForwardHints(os.Stdout, res.Forwards)
			return nil
		},
	}
}
