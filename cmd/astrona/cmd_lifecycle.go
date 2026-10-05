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
	for _, r := range collectKindRows() {
		if !strings.HasPrefix(r.name, "astro-test-") && want(r.status) {
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
		Use:   "stop [lab-name]",
		Short: "Pause a kind lab (frees CPU/RAM, keeps everything) — resume with astrona start",
		Long: "Stop a kind lab's node containers and pause its port forwards. Nothing is deleted: " +
			"`astrona start` brings the cluster, its workloads and its port forwards back.\n\n" +
			"Not supported for labs with more than one control plane — their node IPs change on " +
			"restart, which breaks etcd. qemu labs aren't supported yet.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := resolveLifecycleLab(firstArg(args), flags, func(s string) bool { return s != "Stopped" }, "stop")
			if err != nil {
				return err
			}
			// Refuse an HA cluster before touching anything else.
			cs, _, err := cluster.KindNodeContainers(lab)
			if err != nil {
				return err
			}
			if err := cluster.CheckRestartable(cs); err != nil {
				return err
			}

			rep, err := ui.NewReporter("stop", lab, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			if portforward.Count(lab) > 0 {
				t := rep.Step("Pause port forwards")
				if _, err := portforward.Pause(lab); err != nil {
					return t.Fail(err)
				}
				t.Done()
			}
			if err := cluster.StopKindCluster(lab, rep); err != nil {
				return err
			}

			rep.Close()
			fmt.Printf("\nLab %s stopped — its cluster, workloads and port forwards are kept.\nResume with: astrona start %s\n", lab, lab)
			return nil
		},
	}
}

func newStartCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "start [lab-name]",
		Short: "Resume a kind lab paused with astrona stop",
		Long: "Start a stopped kind lab's node containers, wait for its API, and restart its port " +
			"forwards.\n\n" +
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

			if err := cluster.StartKindCluster(lab, rep); err != nil {
				return err
			}
			if err := cluster.WaitForDefaultServiceAccount("kind-"+lab, cluster.ExistingKubeconfig(lab), 3*time.Minute, rep); err != nil {
				return err
			}

			var forwards []portforward.Forward
			if saved := savedForwards(lab); len(saved) > 0 {
				rep.Section("Port forwards")
				forwards, err = startLabPortForwards(lab, saved, rep)
				if err != nil {
					rep.Warn("some port forwards could not be restarted — `astrona port-forward start -c <config>`")
				}
			}

			health, _ := kindAPIHealth(lab)
			rep.Close()
			fmt.Printf("\nLab %s started — %s.\n", lab, health)
			if !strings.HasPrefix(health, "Ready") {
				fmt.Printf("Nodes can take a minute to report Ready after a restart — check `astrona list`.\n")
			}
			fmt.Printf("\nConnect:\n    astrona shell %s\n    kubectl --context kind-%s ...\n", lab, lab)
			printPortForwardHints(os.Stdout, forwards)
			return nil
		},
	}
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
