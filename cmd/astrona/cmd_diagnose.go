package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/diagnostics"
	"astrona/internal/hypervisor"
	"astrona/internal/lifecycle"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

const (
	diagnosticsOnFailure = "on-failure"
	diagnosticsAlways    = "always"
	diagnosticsNever     = "never"
	// maxPrintedProblems bounds the unhealthy-pod lines echoed to the
	// terminal/CI log; summary.md has them all.
	maxPrintedProblems = 10
)

func validateDiagnosticsMode(mode string) error {
	switch mode {
	case diagnosticsOnFailure, diagnosticsAlways, diagnosticsNever:
		return nil
	default:
		return fmt.Errorf("--diagnostics must be on-failure, always or never (got '%s')", mode)
	}
}

func wantDiagnostics(mode string, runErr error) bool {
	return mode == diagnosticsAlways || (mode == diagnosticsOnFailure && runErr != nil)
}

// collectDiagnostics gathers env's bundle into dir (default
// ~/.astrona/diagnostics/<lab>-<stamp>) and prints where it went plus the
// unhealthy pods. Never fails the caller — diagnostics are best effort on
// top of whatever already happened.
func collectDiagnostics(env *runtime.LabEnvironment, cfg *config.LabConfig, clusterName, dir string, rep *ui.Reporter) {
	if dir == "" {
		d, err := diagnostics.DefaultDir(clusterName)
		if err != nil {
			rep.Warn("could not collect diagnostics: %s", err)
			return
		}
		dir = d
	}

	var sum diagnostics.Summary
	var err error
	switch env.Type {
	case runtime.RuntimeKind:
		sum, err = diagnostics.CollectKind(diagnostics.Kind{Name: clusterName, KubeContext: env.KubeContext, Kubeconfig: env.Kubeconfig}, dir, rep)
	case runtime.RuntimeQEMU:
		sum, err = diagnostics.CollectQEMU(clusterName, qemuConsoleLogs(cfg, clusterName), dir, rep)
	default:
		return
	}
	if err != nil {
		rep.Warn("could not collect diagnostics: %s", err)
		return
	}
	linked := collectLinkedDiagnostics(clusterName, cfg.KindClusters(), dir, rep)
	printDiagnosticsSummary(os.Stderr, sum)
	printLinkedDiagnostics(os.Stderr, linked)
}

// linkedDiagnostics is one linked cluster's part of a bundle.
type linkedDiagnostics struct {
	cluster string
	sum     diagnostics.Summary
}

// collectLinkedDiagnostics collects every linked cluster (runtime.kind.clusters)
// of the lab running as lab into dir/clusters/<cluster> — a failure there
// (the idp didn't come up) is often why the lab's own checks fail.
func collectLinkedDiagnostics(lab string, labs []config.KindCluster, dir string, rep *ui.Reporter) []linkedDiagnostics {
	var out []linkedDiagnostics
	for _, c := range lifecycle.OwnedClusters(lab, labs) {
		sum, err := diagnostics.CollectKind(diagnostics.Kind{
			Name: c, KubeContext: "kind-" + c, Kubeconfig: cluster.ExistingKubeconfig(c),
		}, filepath.Join(dir, "clusters", c), rep)
		if err != nil {
			rep.Warn("could not collect diagnostics for linked cluster %s: %s", c, err)
			continue
		}
		out = append(out, linkedDiagnostics{cluster: c, sum: sum})
	}
	return out
}

func printLinkedDiagnostics(w io.Writer, linked []linkedDiagnostics) {
	for _, l := range linked {
		fmt.Fprintf(w, "\nLinked cluster %s: %s\n", l.cluster, l.sum.Dir)
		if len(l.sum.Problems) == 0 {
			fmt.Fprintf(w, "  No unhealthy pods. %d warning event(s).\n", l.sum.Warnings)
			continue
		}
		fmt.Fprintf(w, "  Unhealthy pods (%d):\n", len(l.sum.Problems))
		for i, p := range l.sum.Problems {
			if i == maxPrintedProblems {
				fmt.Fprintf(w, "    … %d more in %s/summary.md\n", len(l.sum.Problems)-maxPrintedProblems, l.sum.Dir)
				break
			}
			fmt.Fprintf(w, "    %s\n", p)
		}
	}
}

// qemuConsoleLogs lists every VM of the lab under the names astrona list
// shows them as: the lab name for a single-VM lab, "<lab>-<vm>" otherwise.
func qemuConsoleLogs(cfg *config.LabConfig, clusterName string) []diagnostics.QEMUVM {
	names := []string{clusterName}
	if config.IsMultiVM(cfg.Runtime.QEMU) {
		names = names[:0]
		for _, vm := range cfg.Runtime.QEMU {
			names = append(names, clusterName+"-"+vm.Name)
		}
	}
	var vms []diagnostics.QEMUVM
	for _, n := range names {
		if path, err := hypervisor.ConsoleLogPath(n); err == nil {
			vms = append(vms, diagnostics.QEMUVM{Name: n, ConsoleLog: path})
		}
	}
	return vms
}

func printDiagnosticsSummary(w io.Writer, sum diagnostics.Summary) {
	fmt.Fprintf(w, "\nDiagnostics bundle: %s\n", sum.Dir)
	if len(sum.Problems) == 0 {
		fmt.Fprintf(w, "  No unhealthy pods. %d warning event(s) — see summary.md.\n", sum.Warnings)
		return
	}
	fmt.Fprintf(w, "  Unhealthy pods (%d):\n", len(sum.Problems))
	for i, p := range sum.Problems {
		if i == maxPrintedProblems {
			fmt.Fprintf(w, "    … %d more in summary.md\n", len(sum.Problems)-maxPrintedProblems)
			break
		}
		fmt.Fprintf(w, "    %s\n", p)
	}
	fmt.Fprintf(w, "  %d warning event(s) — see summary.md.\n", sum.Warnings)
}

func newDiagnoseCmd(flags *rootFlags) *cobra.Command {
	var outDir string

	cmd := &cobra.Command{
		Use:               "diagnose [lab-name]",
		ValidArgsFunction: labCompletion(nil),
		Short:             "Collect a debugging bundle (pods, events, logs) from a running lab",
		Long: "Collect a debugging bundle from a running lab into a directory: for kind, nodes, pods, " +
			"workloads, events, describe + logs of every unhealthy pod, `kind export logs`, and a " +
			"summary.md listing what's wrong — and the same for each linked cluster " +
			"(runtime.kind.clusters), under clusters/<cluster>/; for qemu, the VM's serial console log. Secrets, " +
			"ConfigMaps and kubeconfigs are never collected.\n\n" +
			"`astrona test` does the same automatically when it fails (see --diagnostics).\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running kind lab.",
		Example: `  astrona diagnose my-lab
  astrona diagnose my-lab -o ./diag`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab := ""
			if len(args) == 1 && qemuStateExists(config.NormalizeClusterName(args[0])) {
				lab = config.NormalizeClusterName(args[0])
			}

			rep, err := ui.NewReporter("diagnose", firstArg(args), flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			if lab == "" {
				lab, err = resolveKindCluster(firstArg(args), flags)
				if err != nil {
					return err
				}
			}
			if outDir == "" {
				if outDir, err = diagnostics.DefaultDir(lab); err != nil {
					return err
				}
			}

			var sum diagnostics.Summary
			if qemuStateExists(lab) {
				path, err := hypervisor.ConsoleLogPath(lab)
				if err != nil {
					return err
				}
				sum, err = diagnostics.CollectQEMU(lab, []diagnostics.QEMUVM{{Name: lab, ConsoleLog: path}}, outDir, rep)
				if err != nil {
					return err
				}
			} else {
				sum, err = diagnostics.CollectKind(diagnostics.Kind{
					Name: lab, KubeContext: "kind-" + lab, Kubeconfig: cluster.ExistingKubeconfig(lab),
				}, outDir, rep)
				if err != nil {
					return err
				}
			}
			linked := collectLinkedDiagnostics(lab, nil, outDir, rep)
			rep.Close()
			printDiagnosticsSummary(os.Stdout, sum)
			printLinkedDiagnostics(os.Stdout, linked)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outDir, "output", "o", "", "Directory to write the bundle to (default ~/.astrona/diagnostics/<lab>-<timestamp>)")
	return cmd
}
