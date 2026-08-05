package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newDestroyCmd builds `astrona destroy`: run teardown scripts, then tear
// down the lab environment (unless the config says keepCluster: true).
// configPath/fileName are bound to the root command's persistent flags.
func newDestroyCmd(configPath, fileName *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down a lab environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			finalPath, err := ResolveConfigPath(*configPath, *fileName)
			if err != nil {
				return fmt.Errorf("path resolution failed: %w", err)
			}

			fmt.Printf("Loading configuration from: %s\n", finalPath)

			clusterName := "astrona-lab"
			var teardown TeardownConfig
			var runtimeCfg RuntimeConfig
			baseDir := filepath.Dir(finalPath)

			if finalPath != "" {
				config, configCleanup, err := LoadLabConfig(finalPath)
				if err == nil {
					defer configCleanup()
					if config.Metadata.Name != "" {
						clusterName = config.Metadata.Name
					}
					teardown = config.Teardown
					runtimeCfg = config.Runtime
				}
			}

			if len(teardown.Init) > 0 {
				fmt.Printf("Running teardown scripts...\n")
				executor := ScriptExecutor(LocalExecutor{})
				if env, err := LoadEnvironment(clusterName, runtimeCfg); err == nil {
					executor = env.Executor
				} else {
					fmt.Printf("[WARN] could not reach lab environment for teardown scripts, running on host instead: %s\n", err)
				}
				if err := RunInitScripts(teardown.Init, baseDir, executor); err != nil {
					fmt.Printf("[WARN] teardown scripts failed: %s\n", err)
				}
			}

			if teardown.KeepCluster {
				fmt.Printf("keepCluster is set, leaving cluster '%s' running.\n", clusterName)
				return nil
			}

			if err := DestroyEnvironment(clusterName, runtimeCfg); err != nil {
				return fmt.Errorf("lab teardown failed: %w", err)
			}

			fmt.Printf("Lab cluster cleaned up successfully.")
			return nil
		},
	}

	return cmd
}
