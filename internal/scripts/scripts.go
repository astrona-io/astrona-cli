package scripts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// MaxScriptDownloadBytes bounds a single downloaded bootstrap/testing/
// teardown/validation script — these are plain text, so a generous cap
// still leaves plenty of headroom while stopping an unbounded download.
const MaxScriptDownloadBytes = 50 * 1024 * 1024

// RunInitScripts runs a list of bootstrap/testing/teardown scripts in order
// through a single executor (bash on the host for kind, SSH into one VM for
// qemu). Each script is a local file (resolved relative to baseDir), a local
// folder (every file inside run in filename order — os.ReadDir already
// returns entries sorted by name, so numbering them e.g. "01-x.sh", "02-y.sh"
// controls run order), or a URL (downloaded to a temp file first). This is
// the low-level primitive — see runBootstrap/runOnEveryVM, below, for
// running against a whole (possibly multi-VM) LabEnvironment instead of one
// fixed executor.
func RunInitScripts(scripts []config.ResourceItem, baseDir string, exec executor.ScriptExecutor, rep *ui.Reporter) error {
	for _, s := range scripts {
		if s.Source == "" {
			continue
		}

		t := rep.Step("%s", s.Name)
		if s.Description != "" {
			rep.Info("%s", s.Description)
		}

		switch strings.ToLower(s.Type) {
		case "url":
			tmpPath, cleanup, err := config.DownloadToTemp(s.Source, "astrona-script-*.sh", MaxScriptDownloadBytes)
			if err != nil {
				return t.Fail(fmt.Errorf("failed to download script from %s: %w", s.Source, err))
			}

			err = exec.RunScript(tmpPath, t.Output())
			cleanup()

			if err != nil {
				return t.Fail(fmt.Errorf("failed to run script '%s': %w", s.Name, err))
			}
		case "file":
			resolved, err := config.JoinWithinBaseDir(baseDir, s.Source)
			if err != nil {
				return t.Fail(fmt.Errorf("failed to resolve script path for '%s': %w", s.Name, err))
			}

			if _, err := os.Stat(resolved); os.IsNotExist(err) {
				return t.Fail(fmt.Errorf("local script file does not exist %s: %w", resolved, err))
			}

			if err := exec.RunScript(resolved, t.Output()); err != nil {
				return t.Fail(fmt.Errorf("failed to run script '%s': %w", s.Name, err))
			}
		case "folder":
			resolved, err := config.JoinWithinBaseDir(baseDir, s.Source)
			if err != nil {
				return t.Fail(fmt.Errorf("failed to resolve script folder for '%s': %w", s.Name, err))
			}

			info, err := os.Stat(resolved)
			if err != nil || !info.IsDir() {
				return t.Fail(fmt.Errorf("local script folder does not exist or is not a directory: %s", resolved))
			}

			entries, err := os.ReadDir(resolved)
			if err != nil {
				return t.Fail(fmt.Errorf("failed to read script folder '%s': %w", resolved, err))
			}

			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}

				scriptFile := filepath.Join(resolved, entry.Name())
				if err := exec.RunScript(scriptFile, t.Output()); err != nil {
					return t.Fail(fmt.Errorf("failed to run script '%s' in folder '%s': %w", entry.Name(), s.Name, err))
				}
			}
		default:
			return t.Fail(fmt.Errorf("unsupported type '%s' for init scripts '%s' (must be 'file', 'folder', or 'url')", s.Type, s.Name))
		}

		t.Done()
	}

	return nil
}

// HasBootstrapInit reports whether RunBootstrap would actually run anything
// for cfg: either the shared root bootstrap.init, or (for a qemu lab) any
// VM's own nested bootstrap.init. Callers gate the "Running bootstrap init
// scripts..." message and the RunBootstrap call itself on this — checking
// len(cfg.Bootstrap.Init) alone misses labs that put all their setup in a
// per-VM nested block with nothing at the root, silently skipping it.
func HasBootstrapInit(cfg *config.LabConfig) bool {
	if len(cfg.Bootstrap.Init) > 0 {
		return true
	}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Bootstrap != nil && len(vm.Bootstrap.Init) > 0 {
			return true
		}
	}
	return false
}

// RunBootstrap runs a lab's bootstrap phase against env: config.Bootstrap.Init
// once via env.Executor for a single-environment lab (kind, or qemu with no
// multi-VM list), or — for a multi-VM qemu lab — once per VM (env.Executor
// is nil precisely then, see LabEnvironment) via that VM's own executor,
// followed by that VM's own nested QEMUVM.Bootstrap.Init if it has one. The
// per-VM order (shared first, then that VM's own) means a VM-specific step
// can assume whatever the shared setup already did.
func RunBootstrap(cfg *config.LabConfig, baseDir string, env *runtime.LabEnvironment, rep *ui.Reporter) error {
	if env.Executor != nil {
		return RunInitScripts(cfg.Bootstrap.Init, baseDir, env.Executor, rep)
	}

	for _, vm := range cfg.Runtime.QEMU {
		exec, err := env.ExecutorForVM(vm.Name)
		if err != nil {
			return err
		}

		rep.Section("Machine: %s", vm.Name)

		if err := RunInitScripts(cfg.Bootstrap.Init, baseDir, exec, rep); err != nil {
			return fmt.Errorf("vm '%s': %w", vm.Name, err)
		}

		if vm.Bootstrap != nil {
			if err := RunInitScripts(vm.Bootstrap.Init, baseDir, exec, rep); err != nil {
				return fmt.Errorf("vm '%s' bootstrap: %w", vm.Name, err)
			}
		}
	}

	return nil
}

// RunOnEveryVM runs scripts once via env.Executor for a single-environment
// lab, or once per VM (in vms' order) for a multi-VM qemu lab — used for
// testing.init and teardown.init, neither of which supports a per-VM
// nested override the way bootstrap.init does via RunBootstrap.
func RunOnEveryVM(scripts []config.ResourceItem, baseDir string, env *runtime.LabEnvironment, vms []config.QEMUVM, rep *ui.Reporter) error {
	if env.Executor != nil {
		return RunInitScripts(scripts, baseDir, env.Executor, rep)
	}

	for _, vm := range vms {
		exec, err := env.ExecutorForVM(vm.Name)
		if err != nil {
			return err
		}
		rep.Section("Machine: %s", vm.Name)
		if err := RunInitScripts(scripts, baseDir, exec, rep); err != nil {
			return fmt.Errorf("vm '%s': %w", vm.Name, err)
		}
	}

	return nil
}

// ResolveLocalSource turns a ResourceItem into a path kubectl/bash can use
// directly: a local file/folder is joined against baseDir and checked to
// exist, a URL is passed through unchanged (kubectl can apply URLs itself).
func ResolveLocalSource(item config.ResourceItem, baseDir string) (string, error) {
	switch strings.ToLower(item.Type) {
	case "file", "folder":
		path, err := config.JoinWithinBaseDir(baseDir, item.Source)
		if err != nil {
			return "", fmt.Errorf("failed to resolve %s source for '%s': %w", item.Type, item.Name, err)
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			return "", fmt.Errorf("local %s source does not exist: %s", item.Type, path)
		}

		return path, nil
	case "url":
		return item.Source, nil
	default:
		return "", fmt.Errorf("unsupported type '%s' for '%s' (must be 'file', 'folder', or 'url')", item.Type, item.Name)
	}
}
