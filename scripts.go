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

// RunInitScripts runs a list of bootstrap/testing/teardown scripts in order.
// Each script is either a local file (resolved relative to baseDir) or a
// URL (downloaded to a temp file first), then run through executor (bash on
// the host for kind, SSH into the VM for qemu).
func RunInitScripts(scripts []ResourceItem, baseDir string, executor ScriptExecutor) error {
	for i, s := range scripts {
		if s.Source == "" {
			continue
		}

		fmt.Printf(" [%d/%d] Init: %s\n", i+1, len(scripts), s.Name)
		if s.Description != "" {
			fmt.Printf("[INFO] %s\n", s.Description)
		}

		scriptPath := s.Source
		var cleanup func() = func() {}

		switch strings.ToLower(s.Type) {
		case "url":
			tmpPath, clean, err := downloadToTemp(s.Source, "astrona-script-*.sh", maxScriptDownloadBytes)
			if err != nil {
				return fmt.Errorf("failed to download script from %s: %w", s.Source, err)
			}

			scriptPath = tmpPath
			cleanup = clean
		case "file":
			resolved, err := joinWithinBaseDir(baseDir, s.Source)
			if err != nil {
				return fmt.Errorf("failed to resolve script path for '%s': %w", s.Name, err)
			}
			scriptPath = resolved

			if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
				return fmt.Errorf("local script file does not exist %s: %w", scriptPath, err)
			}
		default:
			return fmt.Errorf("unsupported type '%s' for init scripts '%s' (must be 'file' or 'folder')", s.Type, s.Name)
		}

		err := executor.RunScript(scriptPath)
		cleanup()

		if err != nil {
			return fmt.Errorf("failed to run script '%s': %w", s.Name, err)
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

// downloadToTemp fetches a URL to a temp file and marks it executable. The
// returned cleanup func removes the temp file once the caller is done.
// Only https:// URLs are accepted, and the body is capped at maxBytes so an
// untrusted remote source can't make this CLI write an unbounded amount of
// data to disk.
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

	os.Chmod(tmpFile.Name(), 0755)

	return tmpFile.Name(), cleanup, nil
}
