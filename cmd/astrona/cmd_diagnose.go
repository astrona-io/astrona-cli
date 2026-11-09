package main

import (
	"fmt"
	"os"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

const (
	diagnosticsOnFailure = "on-failure"
	diagnosticsAlways    = "always"
	diagnosticsNever     = "never"
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
// unhealthy pods on stderr (labops.CollectDiagnostics; the collectors live
// in internal/labops, diagnose.go). S15 removes this wrapper.
func collectDiagnostics(env *runtime.LabEnvironment, cfg *config.LabConfig, clusterName, dir string, rep *ui.Reporter) {
	labops.CollectDiagnostics(os.Stderr, env, cfg, clusterName, dir, rep)
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

			rep, err := ui.New("diagnose", firstArg(args), ui.Options{Verbose: flags.verbose, Plain: flags.headless})
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
			res, err := labops.Diagnose(lab, outDir, rep)
			if err != nil {
				return err
			}
			rep.Close()
			labops.WriteDiagnosticsSummary(os.Stdout, res.Summary)
			labops.WriteLinkedDiagnostics(os.Stdout, res.Linked)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outDir, "output", "o", "", "Directory to write the bundle to (default ~/"+apphome.Name()+"/diagnostics/<lab>-<timestamp>)")
	return cmd
}
