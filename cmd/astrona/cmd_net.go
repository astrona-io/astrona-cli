package main

import (
	"fmt"
	"os/exec"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

func newNetCmd(flags *rootFlags) *cobra.Command {
	var clusterFlag string
	var w config.WANConditions
	var partition, heal, show bool

	cmd := &cobra.Command{
		Use:               "net [lab-name]",
		ValidArgsFunction: labCompletion(isKind),
		Short:             "Change network conditions of a running lab's cluster (latency, loss, partition)",
		Long: "Change the network conditions of one cluster of a running kind lab — a linked cluster " +
			"(--cluster <name>, runtime.kind.labs) or, without --cluster, the lab's own — to simulate " +
			"a slow or flaky remote site, or cut it off entirely:\n\n" +
			"  --latency/--jitter/--loss/--rate   set conditions (tc netem on every node of the cluster)\n" +
			"  --partition                        drop everything it sends (100% loss) — it's unreachable\n" +
			"  --heal                             back to the lab config's wan conditions (or none)\n" +
			"  --show                             print what each node currently has\n\n" +
			"Latency is added once per round trip (it delays what the cluster sends). Changes last " +
			"until `--heal`, `astrona start` (which re-applies the config) or the lab is destroyed. " +
			"While a cluster is partitioned, astrona can't reach its API either.",
		Example: `  astrona net my-lab --cluster idp --latency 300ms --jitter 50ms --loss 5%
  astrona net my-lab --cluster idp --partition
  astrona net my-lab --cluster idp --heal`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			set := !w.IsZero()
			n := 0
			for _, b := range []bool{set, partition, heal, show} {
				if b {
					n++
				}
			}
			if n != 1 {
				return fmt.Errorf("pick one: conditions (--latency/--jitter/--loss/--rate), --partition, --heal or --show")
			}
			lab, err := resolveKindLab(firstArg(args), flags)
			if err != nil {
				return err
			}
			target, configured, err := netTarget(lab, clusterFlag)
			if err != nil {
				return err
			}

			if show {
				return showNet(target)
			}
			switch {
			case partition:
				w = config.WANConditions{Loss: "100%"}
			case heal:
				w = configured
			}
			if err := w.Validate(); err != nil {
				return err
			}

			rep, err := ui.NewReporter("net", lab, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()
			t := rep.Step("Network conditions on %s: %s", target, describeWAN(w))
			if err := cluster.ApplyWAN(target, w); err != nil {
				return t.Fail(err)
			}
			t.Done()
			rep.Close()
			switch {
			case partition:
				fmt.Printf("\n%s is partitioned — nothing it sends gets through. Undo with --heal.\n", target)
			case heal:
				fmt.Printf("\n%s is back to its configured conditions (%s).\n", target, describeWAN(w))
			default:
				fmt.Printf("\n%s now has %s. Undo with --heal.\n", target, describeWAN(w))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&clusterFlag, "cluster", "", "The linked cluster (its runtime.kind.labs name); default: the lab's own cluster")
	cmd.Flags().StringVar(&w.Latency, "latency", "", "Latency added per round trip, e.g. 200ms")
	cmd.Flags().StringVar(&w.Jitter, "jitter", "", "Latency variation, e.g. 30ms (needs --latency)")
	cmd.Flags().StringVar(&w.Loss, "loss", "", "Packet loss, e.g. 5%")
	cmd.Flags().StringVar(&w.Rate, "rate", "", "Bandwidth cap, e.g. 1mbit")
	cmd.Flags().BoolVar(&partition, "partition", false, "Cut the cluster off (100% loss)")
	cmd.Flags().BoolVar(&heal, "heal", false, "Back to the configured wan conditions (or none)")
	cmd.Flags().BoolVar(&show, "show", false, "Show each node's current conditions")
	return cmd
}

// netTarget is the cluster `astrona net` acts on, with its configured wan
// conditions (none for the lab's own cluster).
func netTarget(lab, name string) (string, config.WANConditions, error) {
	if name == "" {
		return lab, config.WANConditions{}, nil
	}
	links, _ := cluster.ReadLinks(lab)
	target, err := linkedCluster(lab, name, links)
	if err != nil {
		return "", config.WANConditions{}, err
	}
	for _, l := range links {
		if l.Cluster == target {
			return target, l.WAN, nil
		}
	}
	return target, config.WANConditions{}, nil
}

func showNet(target string) error {
	nodes, engine, err := cluster.KindNodeContainers(target)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.Role != "control-plane" && n.Role != "worker" {
			continue
		}
		out, err := exec.Command(engine.Path, "exec", n.Name, "tc", "qdisc", "show", "dev", "eth0").Output()
		if err != nil {
			return fmt.Errorf("tc on %s: %w", n.Name, err)
		}
		state := "no conditions"
		if line := strings.TrimSpace(string(out)); strings.Contains(line, "netem") {
			state = line[strings.Index(line, "netem"):]
		}
		fmt.Printf("%-40s %s\n", n.Name, state)
	}
	return nil
}
