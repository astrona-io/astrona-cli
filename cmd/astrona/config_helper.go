package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/ui"

	"github.com/mattn/go-isatty"
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
		return nil, "", func() {}, withNoLabHint(err, flags)
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
		return nil, "", func() {}, withNoLabHint(err, flags)
	}
	if err := ensureLabVersion(cfg.AstronaVersion, flags); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	// Say which lab when it came from `astrona use` — it isn't on the
	// command line. Terminal only, so $(…) captures stay clean.
	if flags.fromCurrent && isatty.IsTerminal(os.Stderr.Fd()) {
		fmt.Fprintln(os.Stderr, ui.Paint(os.Stderr, "→ lab "+cfg.Metadata.Name+" (from `astrona use`)", ui.Dim))
	}

	// Typos are only warned about here, so a lab that has always worked
	// keeps working; `astrona validate` / `astrona check` fail on them.
	for _, u := range cfg.UnknownFields {
		ui.Warnf("%s %s (ignored — run `astrona validate`)", finalPath, u)
	}
	for _, d := range cfg.Deprecations {
		ui.Warnf("%s: %s", finalPath, d)
	}

	applyBundleImages(cfg, filepath.Dir(finalPath))
	return cfg, filepath.Dir(finalPath), cleanup, nil
}

// requireRunningKindLab fails clearly when a kind lab isn't running — or
// when the container engine itself isn't (which hides every lab).
func requireRunningKindLab(cfg *config.LabConfig, clusterName string) error {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return nil
	}
	if _, err := cluster.DetectContainerEngine(); err != nil {
		return err
	}
	if !kindClusterExists(clusterName) {
		return fmt.Errorf("lab %s isn't running — start it: astrona run", clusterName)
	}
	return nil
}
