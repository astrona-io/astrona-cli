package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"astrona/internal/config"
	"astrona/internal/gitsource"
	"astrona/internal/labstate"
	"astrona/internal/version"
)

// Read the saved source locally. The normal run loader may fetch,
// reset the checkout or install another CLI, so it must not be used here.
func loadSavedLab(name string, st *labstate.State, flags *rootFlags) (*config.LabConfig, string, func(), error) {
	noop := func() {}
	if st == nil || st.Source == nil {
		return nil, "", noop, fmt.Errorf("run %s has no saved config source", name)
	}
	applySource(flags, st.Source)
	path := flags.configPath
	if flags.gitURL != "" {
		repo, err := gitsource.CachedGitConfigSource(flags.gitURL, flags.gitRef)
		if err != nil {
			return nil, "", noop, err
		}
		path, err = config.JoinStrictlyWithinBaseDir(repo, path)
		if err != nil {
			return nil, "", noop, err
		}
	}
	if strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") {
		return nil, "", noop, fmt.Errorf("run %s has only a remote config URL; no local copy is available to reuse. The lab is still running", name)
	}
	path, err := config.ResolveConfigPath(path, flags.fileName, "", "", false)
	if err != nil {
		return nil, "", noop, fmt.Errorf("cannot reopen %s from its local files; restore the saved config or cached repository. The lab is still running: %w", name, err)
	}
	cfg, cleanup, err := config.LoadLabConfig(path)
	if err != nil {
		return nil, "", cleanup, err
	}
	if config.NormalizeClusterName(cfg.Metadata.Name) != name {
		cleanup()
		return nil, "", noop, fmt.Errorf("saved config now names a different lab; refusing to open %s", name)
	}
	if cfg.AstronaVersion != "" {
		constraint, err := version.ParseConstraint(cfg.AstronaVersion)
		if err != nil {
			cleanup()
			return nil, "", noop, fmt.Errorf("astronaVersion: %w", err)
		}
		if current, err := version.Parse(Version); err == nil && !constraint.Allows(current) {
			cleanup()
			return nil, "", noop, fmt.Errorf("lab %s needs astrona %s; reopen it with a compatible CLI. The lab is still running", name, constraint)
		}
	}
	baseDir := filepath.Dir(path)
	applyBundleImages(cfg, baseDir)
	return cfg, baseDir, cleanup, nil
}
