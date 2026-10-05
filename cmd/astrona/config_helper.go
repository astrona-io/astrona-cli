package main

import (
	"fmt"
	"os"
	"path/filepath"

	"astrona/internal/config"
)

// LoadLabForCommand resolves --config/--file (and --git/--git-ref, if set),
// loads the YAML, and returns the lab's base directory for resolving
// relative script/manifest paths. Shared by every command that requires a
// valid config to do anything (run, submit, test) — cmd_destroy.go does NOT
// use this, since destroy must still best-effort tear down even when the
// config can't be loaded.
func LoadLabForCommand(flags *rootFlags) (cfg *config.LabConfig, baseDir string, cleanup func(), err error) {
	finalPath, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, flags.verbose)
	if err != nil {
		return nil, "", func() {}, fmt.Errorf("path resolution failed: %w", err)
	}

	if flags.verbose {
		fmt.Printf("Loading configuration from: %s\n", finalPath)
	}

	cfg, cleanup, err = config.LoadLabConfig(finalPath)
	if err != nil {
		return nil, "", func() {}, fmt.Errorf("failed to load lab config: %w", err)
	}

	// Typos are only warned about here, so a lab that has always worked
	// keeps working; `astrona validate` / `astrona check` fail on them.
	for _, u := range cfg.UnknownFields {
		fmt.Fprintf(os.Stderr, "[WARN] %s %s (ignored — run `astrona validate`)\n", finalPath, u)
	}

	return cfg, filepath.Dir(finalPath), cleanup, nil
}
