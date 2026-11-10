// Package apphome separates development state from installed releases.
package apphome

import (
	"fmt"
	"os"
	"path/filepath"

	"astrona/internal/version"
)

var development bool

// Configure selects the profile from the binary's build version. Call it once
// at process startup, before constructing commands or starting goroutines.
// Unversioned builds use development state; tagged releases keep their paths.
func Configure(buildVersion string) {
	_, err := version.Parse(buildVersion)
	development = err != nil
}

// Development reports whether this process uses development state.
func Development() bool { return development }

// Name returns the state directory name beneath the user's home.
func Name() string {
	if development {
		return ".astrona-dev"
	}
	return ".astrona"
}

// ClusterPrefix keeps development clusters outside release discovery, including
// discovery by older releases that match every name beginning with "astro-".
func ClusterPrefix() string {
	if development {
		return "astrodev-"
	}
	return "astro-"
}

// TestClusterPrefix identifies temporary authoring clusters in this profile.
func TestClusterPrefix() string { return ClusterPrefix() + "test-" }

// ReposDir returns the Git cache without creating it. Release paths stay
// unchanged; development caches live with the rest of development state.
func ReposDir() (string, error) {
	if development {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, Name(), "cache", "repos"), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "astrona", "repos"), nil
}

// WorkspaceDir returns the browser workspace's state directory without
// creating it: ~/.astrona/workspace, or the development profile's.
func WorkspaceDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, Name(), "workspace"), nil
}
