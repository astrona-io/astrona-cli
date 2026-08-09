package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

// teardownInfo is what `astrona destroy` needs from the lab config — best
// effort, since destroy must still run even if the config is missing or
// unreadable (e.g. the lab directory was already cleaned up).
type teardownInfo struct {
	clusterName string
	teardown    TeardownConfig
	runtime     RuntimeConfig
}

// loadTeardownInfo tries to load finalPath for the extra info destroy can
// use (cluster name, teardown scripts, runtime type), but never fails: a
// missing or broken config just means destroy falls back to defaults.
func loadTeardownInfo(finalPath string) (teardownInfo, func()) {
	info := teardownInfo{clusterName: "astrona-lab"}
	cleanup := func() {}

	if finalPath == "" {
		return info, cleanup
	}

	config, configCleanup, err := LoadLabConfig(finalPath)
	if err != nil {
		return info, cleanup
	}
	cleanup = configCleanup

	if config.Metadata.Name != "" {
		info.clusterName = config.Metadata.Name
	}
	info.teardown = config.Teardown
	info.runtime = config.Runtime

	return info, cleanup
}

// teardownExecutor picks what runs the teardown scripts: the lab's real
// environment if it's still reachable, otherwise the host shell — teardown
// scripts should still get a best-effort run even if the cluster/VM is
// already gone.
func teardownExecutor(clusterName string, runtimeCfg RuntimeConfig) ScriptExecutor {
	env, err := LoadEnvironment(clusterName, runtimeCfg)
	if err != nil {
		fmt.Printf("[WARN] could not reach lab environment for teardown scripts, running on host instead: %s\n", err)
		return LocalExecutor{}
	}
	return env.Executor
}

// newDestroyCmd builds `astrona destroy`: run teardown scripts, then tear
// down the lab environment (unless the config says keepCluster: true).
// flags is bound to the root command's persistent flags.
func newDestroyCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down a lab environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			finalPath, err := ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef)
			if err != nil {
				return fmt.Errorf("path resolution failed: %w", err)
			}

			fmt.Printf("Loading configuration from: %s\n", finalPath)

			baseDir := filepath.Dir(finalPath)
			info, configCleanup := loadTeardownInfo(finalPath)
			defer configCleanup()

			if len(info.teardown.Init) > 0 {
				fmt.Printf("Running teardown scripts...\n")
				executor := teardownExecutor(info.clusterName, info.runtime)
				if err := RunInitScripts(info.teardown.Init, baseDir, executor); err != nil {
					fmt.Printf("[WARN] teardown scripts failed: %s\n", err)
				}
			}

			if info.teardown.KeepCluster {
				fmt.Printf("keepCluster is set, leaving cluster '%s' running.\n", info.clusterName)
				return nil
			}

			if err := DestroyEnvironment(info.clusterName, info.runtime); err != nil {
				return fmt.Errorf("lab teardown failed: %w", err)
			}

			fmt.Printf("Lab cluster cleaned up successfully.\n")
			return nil
		},
	}

	return cmd
}
