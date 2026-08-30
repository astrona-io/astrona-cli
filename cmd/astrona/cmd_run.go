package main

import (
	"fmt"

	"astrona/internal/config"
	"astrona/internal/manifests"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// newRunCmd builds `astrona run`: create the lab environment (kind cluster
// or qemu VM), then run bootstrap (init scripts + apply manifests). flags
// is bound to the root command's persistent --config/--file/--git/--git-ref
// flags.
func newRunCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Spin up a lab environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.configPath == "" {
				return fmt.Errorf("please specify a configuration file using --config or -c")
			}

			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			rep, err := ui.NewReporter("run", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			rep.Section("Lab: %s", cfg.Metadata.Name)

			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)

			env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime, rep)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}

			if scripts.HasBootstrapInit(cfg) {
				rep.Section("Bootstrap")
				if err := scripts.RunBootstrap(cfg, baseDir, env, rep); err != nil {
					return fmt.Errorf("init scripts failed: %w", err)
				}
			}

			if len(cfg.Bootstrap.Manifests) > 0 {
				if env.KubeContext == "" {
					return fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
				}
				rep.Section("Manifests")
				if err := manifests.ApplyManifests(cfg.Bootstrap.Manifests, baseDir, env.KubeContext, rep); err != nil {
					return fmt.Errorf("bootstrap manifests failed: %w", err)
				}
			}

			rep.Close()
			fmt.Printf("\nLab environment is fully loaded and ready!\n")
			printConnectHints(env, cfg, clusterName)
			fmt.Printf("Full log: %s\n", rep.LogPath())
			return nil
		},
	}

	return cmd
}

// printConnectHints prints, for a qemu lab, the paste-ready `astrona ssh`
// command to get a shell on each VM. No-op for a kind lab, which has no VM
// to SSH into.
func printConnectHints(env *runtime.LabEnvironment, cfg *config.LabConfig, clusterName string) {
	if env.Type != runtime.RuntimeQEMU {
		return
	}

	// The name a VM is addressed under: the lab name for a single-VM lab,
	// "<lab>-<vm>" for each VM of a multi-VM lab — exactly what `astrona ssh`
	// / `astrona list` expect.
	fmt.Printf("\nConnect:\n")
	if config.IsMultiVM(cfg.Runtime.QEMU) {
		for _, vm := range cfg.Runtime.QEMU {
			fmt.Printf("    astrona ssh %s-%s\n", clusterName, vm.Name)
		}
		return
	}
	fmt.Printf("    astrona ssh %s\n", clusterName)
}
