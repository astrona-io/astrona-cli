package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"astrona/internal/config"
)

// withNoLabHint adds what to do when no lab was named and the current
// directory has none — the usual first stumble.
func withNoLabHint(err error, flags *rootFlags) error {
	if errors.Is(err, os.ErrNotExist) && flags.configPath == "." && flags.gitURL == "" {
		return fmt.Errorf("%w\nNo lab here — name one (`astrona run ./path/to/lab`, -c, --git) or pick one for every command: astrona use <lab>", err)
	}
	return err
}

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
		// A config for another astrona may not parse here: hand over first.
		if want := labVersionFromLoadError(err); want != "" {
			if verr := ensureLabVersion(want, flags); verr != nil {
				return nil, "", func() {}, verr
			}
		}
		return nil, "", func() {}, withNoLabHint(fmt.Errorf("failed to load lab config: %w", err), flags)
	}
	if err := ensureLabVersion(cfg.AstronaVersion, flags); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}

	// Typos are only warned about here, so a lab that has always worked
	// keeps working; `astrona validate` / `astrona check` fail on them.
	for _, u := range cfg.UnknownFields {
		fmt.Fprintf(os.Stderr, "[WARN] %s %s (ignored — run `astrona validate`)\n", finalPath, u)
	}
	for _, d := range cfg.Deprecations {
		fmt.Fprintf(os.Stderr, "[WARN] %s: %s\n", finalPath, d)
	}

	applyBundleImages(cfg, filepath.Dir(finalPath))
	return cfg, filepath.Dir(finalPath), cleanup, nil
}
