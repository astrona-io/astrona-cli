package main

import (
	"fmt"

	"astrona/internal/config"
	"astrona/internal/manifests"
	"astrona/internal/runtime"
	"astrona/internal/scripts"

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

			fmt.Printf("Initializing Lab: %s\n", cfg.Metadata.Name)

			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)

			env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}
			fmt.Printf("Lab environment is ready\n")

			if scripts.HasBootstrapInit(cfg) {
				fmt.Printf("Running bootstrap init scripts...\n")
				if err := scripts.RunBootstrap(cfg, baseDir, env); err != nil {
					return fmt.Errorf("init scripts failed: %w", err)
				}
			}

			if len(cfg.Bootstrap.Manifests) > 0 {
				if env.KubeContext == "" {
					return fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
				}
				fmt.Printf("Applying bootstrap manifests...\n")
				if err := manifests.ApplyManifests(cfg.Bootstrap.Manifests, baseDir, env.KubeContext); err != nil {
					return fmt.Errorf("bootstrap manifests failed: %w", err)
				}
			}

			fmt.Printf("\nLab environment is fully loaded and ready!\n")
			return nil
		},
	}

	return cmd
}
