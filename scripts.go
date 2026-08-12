package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxScriptDownloadBytes bounds a single downloaded bootstrap/testing/
// teardown/validation script — these are plain text, so a generous cap
// still leaves plenty of headroom while stopping an unbounded download.
const maxScriptDownloadBytes = 50 * 1024 * 1024

// downloadHTTPTimeout bounds the whole request (connect + redirects +
// reading the body) so a hung or slow-drip remote source can't block the
// CLI indefinitely.
const downloadHTTPTimeout = 30 * time.Minute

// RunInitScripts runs a list of bootstrap/testing/teardown scripts in order
// through a single executor (bash on the host for kind, SSH into one VM for
// qemu). Each script is a local file (resolved relative to baseDir), a local
// folder (every file inside run in filename order — os.ReadDir already
// returns entries sorted by name, so numbering them e.g. "01-x.sh", "02-y.sh"
// controls run order), or a URL (downloaded to a temp file first). This is
// the low-level primitive — see runBootstrap/runOnEveryVM, below, for
// running against a whole (possibly multi-VM) LabEnvironment instead of one
// fixed executor.
func RunInitScripts(scripts []ResourceItem, baseDir string, executor ScriptExecutor) error {
	for i, s := range scripts {
		if s.Source == "" {
			continue
		}

		fmt.Printf(" [%d/%d] Init: %s\n", i+1, len(scripts), s.Name)
		if s.Description != "" {
			fmt.Printf("[INFO] %s\n", s.Description)
		}

		switch strings.ToLower(s.Type) {
		case "url":
			tmpPath, cleanup, err := downloadToTemp(s.Source, "astrona-script-*.sh", maxScriptDownloadBytes)
			if err != nil {
				return fmt.Errorf("failed to download script from %s: %w", s.Source, err)
			}

			err = executor.RunScript(tmpPath)
			cleanup()

			if err != nil {
				return fmt.Errorf("failed to run script '%s': %w", s.Name, err)
			}
		case "file":
			resolved, err := joinWithinBaseDir(baseDir, s.Source)
			if err != nil {
				return fmt.Errorf("failed to resolve script path for '%s': %w", s.Name, err)
			}

			if _, err := os.Stat(resolved); os.IsNotExist(err) {
				return fmt.Errorf("local script file does not exist %s: %w", resolved, err)
			}

			if err := executor.RunScript(resolved); err != nil {
				return fmt.Errorf("failed to run script '%s': %w", s.Name, err)
			}
		case "folder":
			resolved, err := joinWithinBaseDir(baseDir, s.Source)
			if err != nil {
				return fmt.Errorf("failed to resolve script folder for '%s': %w", s.Name, err)
			}

			info, err := os.Stat(resolved)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("local script folder does not exist or is not a directory: %s", resolved)
			}

			entries, err := os.ReadDir(resolved)
			if err != nil {
				return fmt.Errorf("failed to read script folder '%s': %w", resolved, err)
			}

			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}

				scriptFile := filepath.Join(resolved, entry.Name())
				if err := executor.RunScript(scriptFile); err != nil {
					return fmt.Errorf("failed to run script '%s' in folder '%s': %w", entry.Name(), s.Name, err)
				}
			}
		default:
			return fmt.Errorf("unsupported type '%s' for init scripts '%s' (must be 'file', 'folder', or 'url')", s.Type, s.Name)
		}
	}

	return nil
}

// runBootstrap runs a lab's bootstrap phase against env: config.Bootstrap.Init
// once via env.Executor for a single-environment lab (kind, or qemu with no
// multi-VM list), or — for a multi-VM qemu lab — once per VM (env.Executor
// is nil precisely then, see LabEnvironment) via that VM's own executor,
// followed by that VM's own nested QEMUVM.Bootstrap.Init if it has one. The
// per-VM order (shared first, then that VM's own) means a VM-specific step
// can assume whatever the shared setup already did.
func runBootstrap(config *LabConfig, baseDir string, env *LabEnvironment) error {
	if env.Executor != nil {
		return RunInitScripts(config.Bootstrap.Init, baseDir, env.Executor)
	}

	for _, vm := range config.Runtime.QEMU {
		executor, err := env.executorForVM(vm.Name)
		if err != nil {
			return err
		}

		if err := RunInitScripts(config.Bootstrap.Init, baseDir, executor); err != nil {
			return fmt.Errorf("vm '%s': %w", vm.Name, err)
		}

		if vm.Bootstrap != nil {
			if err := RunInitScripts(vm.Bootstrap.Init, baseDir, executor); err != nil {
				return fmt.Errorf("vm '%s' bootstrap: %w", vm.Name, err)
			}
		}
	}

	return nil
}

// runOnEveryVM runs scripts once via env.Executor for a single-environment
// lab, or once per VM (in vms' order) for a multi-VM qemu lab — used for
// testing.init and teardown.init, neither of which supports a per-VM
// nested override the way bootstrap.init does via runBootstrap.
func runOnEveryVM(scripts []ResourceItem, baseDir string, env *LabEnvironment, vms []QEMUVM) error {
	if env.Executor != nil {
		return RunInitScripts(scripts, baseDir, env.Executor)
	}

	for _, vm := range vms {
		executor, err := env.executorForVM(vm.Name)
		if err != nil {
			return err
		}
		if err := RunInitScripts(scripts, baseDir, executor); err != nil {
			return fmt.Errorf("vm '%s': %w", vm.Name, err)
		}
	}

	return nil
}

// resolveLocalSource turns a ResourceItem into a path kubectl/bash can use
// directly: a local file/folder is joined against baseDir and checked to
// exist, a URL is passed through unchanged (kubectl can apply URLs itself).
func resolveLocalSource(item ResourceItem, baseDir string) (string, error) {
	switch strings.ToLower(item.Type) {
	case "file", "folder":
		path, err := joinWithinBaseDir(baseDir, item.Source)
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

// joinWithinBaseDir resolves a possibly-relative source path against
// baseDir and rejects any result that escapes baseDir (e.g. via "../..").
// baseDir's own config can come from an untrusted remote URL
// (LoadLabConfig), so a relative source path is attacker-influenced and
// must not be allowed to reach files outside the lab directory. Absolute
// paths are passed through unchanged — that's an explicit, visible choice
// in the config, not a traversal.
func joinWithinBaseDir(baseDir, source string) (string, error) {
	if filepath.IsAbs(source) {
		return source, nil
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory '%s': %w", baseDir, err)
	}

	joined := filepath.Join(absBase, source)

	rel, err := filepath.Rel(absBase, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path '%s' escapes lab directory '%s'", source, absBase)
	}

	return joined, nil
}

// downloadToTemp fetches a URL to a temp file. The returned cleanup func
// removes the temp file once the caller is done. Only https:// URLs are
// accepted, and the body is capped at maxBytes so an untrusted remote
// source can't make this CLI write an unbounded amount of data to disk.
// The file is never chmod +x: LocalExecutor runs it as `bash scriptPath`
// and SSHExecutor pipes it over stdin to `bash -s` — neither ever executes
// it directly, so there's no reason to set the executable bit.
func downloadToTemp(url, filePattern string, maxBytes int64) (string, func(), error) {
	cleanup := func() {}

	if !strings.HasPrefix(url, "https://") {
		return "", cleanup, fmt.Errorf("refusing to download from non-https URL '%s': only https:// sources are allowed", url)
	}

	client := &http.Client{Timeout: downloadHTTPTimeout}

	resp, err := client.Get(url)
	if err != nil {
		return "", cleanup, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", cleanup, fmt.Errorf("server returned status: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", filePattern)
	if err != nil {
		return "", cleanup, err
	}

	cleanup = func() {
		os.Remove(tmpFile.Name())
	}

	n, err := io.Copy(tmpFile, io.LimitReader(resp.Body, maxBytes+1))
	tmpFile.Close()
	if err != nil {
		cleanup()
		return "", cleanup, err
	}

	if n > maxBytes {
		cleanup()
		return "", func() {}, fmt.Errorf("download from '%s' exceeds %d byte limit", url, maxBytes)
	}

	return tmpFile.Name(), cleanup, nil
}
