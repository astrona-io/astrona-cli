package main

import (
	"fmt"

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

			config, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			fmt.Printf("Initializing Lab: %s\n", config.Metadata.Name)

			clusterName := config.Metadata.Name
			if clusterName == "" {
				clusterName = "astrona-lab"
			}

			env, err := CreateEnvironment(clusterName, baseDir, config.Runtime)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}
			fmt.Printf("Lab environment is ready\n")

			if len(config.Bootstrap.Init) > 0 {
				fmt.Printf("Running bootstrap init scripts...\n")
				if err := runBootstrap(config, baseDir, env); err != nil {
					return fmt.Errorf("init scripts failed: %w", err)
				}
			}

			if len(config.Bootstrap.Manifests) > 0 {
				if env.KubeContext == "" {
					return fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
				}
				fmt.Printf("Applying bootstrap manifests...\n")
				if err := ApplyManifests(config.Bootstrap.Manifests, baseDir, env.KubeContext); err != nil {
					return fmt.Errorf("bootstrap manifests failed: %w", err)
				}
			}

			fmt.Printf("\nLab environment is fully loaded and ready!\n")
			return nil
		},
	}

	return cmd
}
