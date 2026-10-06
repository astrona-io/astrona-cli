package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"astrona/internal/config"
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

// confirmReset asks on in/out whether to throw lab away. Only "y"/"yes"
// (any case) confirms.
func confirmReset(in io.Reader, out io.Writer, lab string) bool {
	fmt.Fprintf(out, "Reset %s? This deletes the lab and everything done in it, then recreates it from the config. [y/N] ", lab)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func newResetCmd(flags *rootFlags) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Start a lab over: destroy it and recreate it from its config",
		Long: "Start a lab over from scratch: run its teardown scripts, destroy it, then do everything " +
			"`astrona run` does — the lab ends up exactly as a fresh `astrona run` leaves it. " +
			"If the lab isn't running, it's simply created.\n\n" +
			"The config is validated before anything is destroyed, so a broken config never leaves " +
			"you without a lab. teardown.keepCluster is ignored — reset always recreates.\n\n" +
			"Asks for confirmation (everything done in the lab is lost); --yes skips it and is " +
			"required when not running in a terminal.",
		Example: `  astrona reset -c ./labs/my-lab
  astrona reset -c ./labs/my-lab --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			if err := requireTrust(flags, cfg, baseDir); err != nil {
				return err
			}

			if err := validateLabForRun(cfg); err != nil {
				return fmt.Errorf("lab config is invalid, nothing was reset: %w", err)
			}

			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
			exists := labExists(cfg, clusterName)

			if exists && !yes {
				if !isatty.IsTerminal(os.Stdin.Fd()) {
					return fmt.Errorf("refusing to reset '%s' without confirmation — pass --yes when not running in a terminal", clusterName)
				}
				if !confirmReset(os.Stdin, os.Stdout, clusterName) {
					fmt.Println("Reset cancelled — nothing was changed.")
					return nil
				}
			}

			rep, err := ui.NewReporter("reset", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			if exists {
				teardown := cfg.Teardown
				teardown.KeepCluster = false
				info := teardownInfo{clusterName: clusterName, teardown: teardown, runtime: cfg.Runtime}
				if err := tearDownLabEnvironment(clusterName, info, baseDir, true, rep); err != nil {
					return fmt.Errorf("could not remove the old lab, nothing was recreated: %w", err)
				}
			} else {
				rep.Info("Lab '%s' isn't running — creating it fresh.", clusterName)
			}

			return bringUpLab(cfg, baseDir, rep)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Don't ask for confirmation")
	return cmd
}
