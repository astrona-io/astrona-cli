package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newRunCmd builds `astrona run`: create the lab environment (kind cluster
// or qemu VM), then run bootstrap (init scripts + apply manifests).
// configPath/fileName are bound to the root command's persistent
// --config/--file flags.
func newRunCmd(configPath, fileName *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Spin up a lab environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			finalPath, err := ResolveConfigPath(*configPath, *fileName)
			if err != nil {
				return fmt.Errorf("path resolution failed: %w", err)
			}

			baseDir := filepath.Dir(finalPath)
			fmt.Printf("Loading configuration from: %s\n", finalPath)

			if *configPath == "" {
				return fmt.Errorf("please specify a configuration file using --config or -c")
			}

			config, configCleanup, err := LoadLabConfig(finalPath)
			if err != nil {
				return fmt.Errorf("failed to load lab config: %w", err)
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
			fmt.Printf("Lab environment is ready")

			if len(config.Bootstrap.Init) > 0 {
				fmt.Printf("Running bootstrap init scripts...\n")
				if err := RunInitScripts(config.Bootstrap.Init, baseDir, env.Executor); err != nil {
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

			fmt.Printf("\nLab environment is fully loaded and ready!")
			return nil
		},
	}

	return cmd
}
