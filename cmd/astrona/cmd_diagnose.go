package main

import (
	"fmt"
	"io"
	"os"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/diagnostics"
	"astrona/internal/hypervisor"
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
	printDiagnosticsSummary(os.Stderr, sum)
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
			"summary.md listing what's wrong; for qemu, the VM's serial console log. Secrets, " +
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
				lab, err = resolveKindLab(firstArg(args), flags)
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
			rep.Close()
			printDiagnosticsSummary(os.Stdout, sum)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outDir, "output", "o", "", "Directory to write the bundle to (default ~/.astrona/diagnostics/<lab>-<timestamp>)")
	return cmd
}
