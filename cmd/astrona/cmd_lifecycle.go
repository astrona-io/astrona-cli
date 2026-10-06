package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// resolveLifecycleLab picks the kind lab `stop`/`start` act on: an
// explicit name, else the lab config from -c/--file/--git, else the only
// astrona kind lab whose status `want` accepts (running ones for stop,
// stopped ones for start). verb is for error messages.
func resolveLifecycleLab(labArg string, flags *rootFlags, want func(status string) bool, verb string) (string, error) {
	check := func(name string) (string, error) {
		if qemuStateExists(name) {
			return "", fmt.Errorf("'%s' is a qemu lab — `astrona %s` only supports kind labs for now", name, verb)
		}
		if !kindClusterExists(name) {
			return "", fmt.Errorf("no kind lab named '%s' — run `astrona list` to see what exists", name)
		}
		return name, nil
	}

	if labArg != "" {
		return check(config.NormalizeClusterName(labArg))
	}
	if cfg, _, cleanup, err := LoadLabForCommand(flags); err == nil {
		defer cleanup()
		if cfg.Runtime.Type != "" && cfg.Runtime.Type != string(runtime.RuntimeKind) {
			return "", fmt.Errorf("lab '%s' uses the %s runtime — `astrona %s` only supports kind labs for now", cfg.Metadata.Name, cfg.Runtime.Type, verb)
		}
		return check(config.NormalizeClusterName(cfg.Metadata.Name))
	}

	var matches []string
	owners := linkedClusterOwners()
	for _, r := range collectKindRows() {
		if _, linked := owners[r.name]; !linked && !strings.HasPrefix(r.name, "astro-test-") && want(r.status) {
			matches = append(matches, r.name)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no astrona kind lab to %s — `astrona list` shows what exists", verb)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("several kind labs match — pick one for `astrona %s`: %s", verb, strings.Join(matches, ", "))
	}
}

func newStopCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:               "stop [lab-name]",
		ValidArgsFunction: labCompletion(func(r labRow) bool { return isKind(r) && isRunning(r) }),
		Short:             "Pause a kind lab (frees CPU/RAM, keeps everything) — resume with astrona start",
		Long: "Stop a kind lab's node containers and pause its port forwards. Nothing is deleted: " +
			"`astrona start` brings the cluster, its workloads and its port forwards back.\n\n" +
			"The lab's linked clusters (runtime.kind.labs) are stopped with it.\n\n" +
			"Not supported for labs with more than one control plane — their node IPs change on " +
			"restart, which breaks etcd. qemu labs aren't supported yet.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := resolveLifecycleLab(firstArg(args), flags, func(s string) bool { return s != "Stopped" }, "stop")
			if err != nil {
				return err
			}

			labs := append([]string{lab}, ownedClusters(lab, nil)...)
			// Refuse an HA cluster before touching anything else.
			for _, l := range labs {
				cs, _, err := cluster.KindNodeContainers(l)
				if err != nil {
					return err
				}
				if err := cluster.CheckRestartable(cs); err != nil {
					return fmt.Errorf("%s: %w", l, err)
				}
			}

			rep, err := ui.NewReporter("stop", lab, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			for _, l := range labs {
				if len(labs) > 1 {
					rep.Section("Lab %s", l)
				}
				if err := stopLab(l, rep); err != nil {
					return err
				}
			}

			rep.Close()
			fmt.Printf("\nLab %s stopped — its cluster, workloads and port forwards are kept.\n", lab)
			if len(labs) > 1 {
				fmt.Printf("Linked clusters stopped too: %s.\n", strings.Join(labs[1:], ", "))
			}
			fmt.Printf("Resume with: astrona start %s\n", lab)
			return nil
		},
	}
}

// stopLab pauses lab's port forwards and stops its node containers.
func stopLab(lab string, rep *ui.Reporter) error {
	if portforward.Count(lab) > 0 {
		t := rep.Step("Pause port forwards")
		if _, err := portforward.Pause(lab); err != nil {
			return t.Fail(err)
		}
		t.Done()
	}
	return cluster.StopKindCluster(lab, rep)
}

func newStartCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:               "start [lab-name]",
		ValidArgsFunction: labCompletion(isStoppedKind),
		Short:             "Resume a kind lab paused with astrona stop",
		Long: "Start a stopped kind lab's node containers, wait for its API, and restart its port " +
			"forwards. Its linked clusters (runtime.kind.labs) are started first — the lab needs them.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only stopped kind lab.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := resolveLifecycleLab(firstArg(args), flags, func(s string) bool {
				return s == "Stopped" || strings.HasPrefix(s, "Degraded")
			}, "start")
			if err != nil {
				return err
			}

			rep, err := ui.NewReporter("start", lab, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			// Linked clusters first — the lab's workloads may call them.
			linked := ownedClusters(lab, nil)
			saved, _ := cluster.ReadLinks(lab)
			for _, c := range linked {
				rep.Section("Linked cluster %s", c)
				if _, err := startLab(c, rep); err != nil {
					return fmt.Errorf("linked cluster %s: %w", c, err)
				}
				// A restart drops netem — re-apply the configured conditions.
				for _, l := range saved {
					if l.Cluster == c {
						if err := applyWANStep(c, l.WAN, rep); err != nil {
							rep.Warn("could not re-apply wan conditions on %s: %s", c, err)
						}
					}
				}
			}
			if len(linked) > 0 {
				rep.Section("Lab %s", lab)
			}
			forwards, err := startLab(lab, rep)
			if err != nil {
				return err
			}
			// Node IPs may have changed with the restart.
			refreshLinkNames(lab, false, rep)

			health, _ := kindAPIHealth(lab)
			rep.Close()
			fmt.Printf("\nLab %s started — %s.\n", lab, health)
			if len(linked) > 0 {
				fmt.Printf("Linked clusters started too: %s.\n", strings.Join(linked, ", "))
			}
			if !strings.HasPrefix(health, "Ready") {
				fmt.Printf("Nodes can take a minute to report Ready after a restart — check `astrona list`.\n")
			}
			fmt.Printf("\nConnect:\n    astrona shell %s\n    kubectl --context kind-%s ...\n", lab, lab)
			printPortForwardHints(os.Stdout, forwards)
			return nil
		},
	}
}

// startLab starts lab's node containers, waits for its API, and restarts
// its saved port forwards.
func startLab(lab string, rep *ui.Reporter) ([]portforward.Forward, error) {
	if err := cluster.StartKindCluster(lab, rep); err != nil {
		return nil, err
	}
	if err := cluster.WaitForDefaultServiceAccount("kind-"+lab, cluster.ExistingKubeconfig(lab), 3*time.Minute, rep); err != nil {
		return nil, err
	}
	saved := savedForwards(lab)
	if len(saved) == 0 {
		return nil, nil
	}
	rep.Section("Port forwards")
	forwards, err := startLabPortForwards(lab, saved, rep)
	if err != nil {
		rep.Warn("some port forwards could not be restarted — `astrona port-forward start -c <config>`")
	}
	return forwards, nil
}

// savedForwards returns the port forward specs recorded for lab (kept by
// portforward.Pause while it's stopped).
func savedForwards(lab string) []config.PortForward {
	fs, err := portforward.List(lab)
	if err != nil {
		return nil
	}
	out := make([]config.PortForward, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Spec.Forward)
	}
	return out
}
