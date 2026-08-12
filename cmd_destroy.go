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

// teardownEnvironment picks what runs the teardown scripts: the lab's real
// environment if it's still reachable, otherwise the host shell — teardown
// scripts should still get a best-effort run even if the cluster/VM is
// already gone. Wrapped in a LabEnvironment (rather than returning a bare
// ScriptExecutor) so RunInitScripts can still resolve per-VM targeting
// (ResourceItem.VM) for a multi-VM qemu lab's teardown scripts.
func teardownEnvironment(clusterName string, runtimeCfg RuntimeConfig) *LabEnvironment {
	env, err := LoadEnvironment(clusterName, runtimeCfg)
	if err != nil {
		fmt.Printf("[WARN] could not reach lab environment for teardown scripts, running on host instead: %s\n", err)
		return &LabEnvironment{Executor: LocalExecutor{}}
	}
	return env
}

// tearDownLabEnvironment runs teardown scripts (if any) then destroys
// clusterName's environment. hardFail controls whether a destroy failure is
// returned to the caller or just logged — used to make the "test-<lab>"
// side-destroy (see newDestroyCmd) best-effort so a missing/already-gone
// test environment never fails `astrona destroy` for the real one.
func tearDownLabEnvironment(clusterName string, info teardownInfo, baseDir string, hardFail bool) error {
	if len(info.teardown.Init) > 0 {
		fmt.Printf("Running teardown scripts for '%s'...\n", clusterName)
		env := teardownEnvironment(clusterName, info.runtime)
		if err := runOnEveryVM(info.teardown.Init, baseDir, env, info.runtime.QEMU); err != nil {
			fmt.Printf("[WARN] teardown scripts failed for '%s': %s\n", clusterName, err)
		}
	}

	if info.teardown.KeepCluster {
		fmt.Printf("keepCluster is set, leaving cluster '%s' running.\n", clusterName)
		return nil
	}

	if err := DestroyEnvironment(clusterName, info.runtime); err != nil {
		if hardFail {
			return fmt.Errorf("lab teardown failed: %w", err)
		}
		fmt.Printf("[WARN] teardown failed for '%s': %s\n", clusterName, err)
	}

	return nil
}

// newDestroyCmd builds `astrona destroy`: run teardown scripts, then tear
// down the lab environment (unless the config says keepCluster: true).
// Also best-effort tears down the "test-<lab>" environment `astrona test`
// uses (see cmd_devtest.go) — a cancelled `astrona test` skips Go's defer
// cleanup entirely (Ctrl-C kills the process before it runs), so without
// this a crashed test run would need its own separate cleanup command. One
// `astrona destroy` is enough to clean up whichever of the two you actually
// have running. flags is bound to the root command's persistent flags.
func newDestroyCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down a lab environment (both the normal run and any leftover 'astrona test' run)",
		RunE: func(cmd *cobra.Command, args []string) error {
			finalPath, err := ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef)
			if err != nil {
				return fmt.Errorf("path resolution failed: %w", err)
			}

			fmt.Printf("Loading configuration from: %s\n", finalPath)

			baseDir := filepath.Dir(finalPath)
			info, configCleanup := loadTeardownInfo(finalPath)
			defer configCleanup()

			if err := tearDownLabEnvironment(info.clusterName, info, baseDir, true); err != nil {
				return err
			}

			if err := tearDownLabEnvironment("test-"+info.clusterName, info, baseDir, false); err != nil {
				// tearDownLabEnvironment(hardFail=false) never actually
				// returns an error, but handle it rather than silently
				// dropping one if that ever changes.
				fmt.Printf("[WARN] %s\n", err)
			}

			fmt.Printf("Lab cluster cleaned up successfully.\n")
			return nil
		},
	}

	return cmd
}
