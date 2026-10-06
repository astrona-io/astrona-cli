package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newUpgradeCmd builds `astrona upgrade`: dynamically checks GitHub for the
// latest release, downloads the raw compiled binary for the active OS and
// architecture, and performs an atomic rename/replace on the currently
// running executable.
func newUpgradeCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade astrona-cli to the latest version",
		Long: "Download the latest release for this OS and architecture, verified against GitHub's SHA-256 " +
			"digest, and replace the running astrona with it — only when it's newer (--force reinstalls).\n\n" +
			"Installed with Homebrew? Use `brew upgrade astrona` instead; this command refuses to replace " +
			"a binary Homebrew manages.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Homebrew owns its install: replacing the binary under its
			// Cellar would leave brew believing the old version is there.
			if exe, err := currentExecutable(); err == nil && isHomebrewInstall(exe) {
				return fmt.Errorf("astrona was installed with Homebrew (%s) — upgrade it with: brew upgrade astrona", exe)
			}

			fmt.Println("Checking for latest version...")

			client := &http.Client{
				Timeout: 10 * time.Second,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}

			resp, err := client.Head("https://github.com/astrona-io/astrona-cli/releases/latest")
			if err != nil {
				return fmt.Errorf("failed to check for latest version: %w", err)
			}
			defer resp.Body.Close()

			location := resp.Header.Get("Location")
			if location == "" {
				return fmt.Errorf("failed to get latest version: redirect location missing")
			}

			parts := strings.Split(strings.TrimRight(location, "/"), "/")
			if len(parts) == 0 {
				return fmt.Errorf("failed to parse latest version from location: %s", location)
			}
			latestTag := parts[len(parts)-1]

			if latestTag == "" {
				return fmt.Errorf("failed to resolve latest version tag")
			}

			if !newerRelease(latestTag, Version) && !force {
				fmt.Printf("You are on %s; the latest release is %s — nothing to upgrade (--force reinstalls it).\n", Version, latestTag)
				return nil
			}

			fmt.Printf("Upgrading from %s to %s...\n", Version, latestTag)

			execPath, err := currentExecutable()
			if err != nil {
				return err
			}
			fmt.Printf("Downloading %s (%s), verified against GitHub's SHA-256 digest...\n", latestTag, releaseAssetName())
			if err := downloadVerifiedRelease(latestTag, execPath); err != nil {
				return fmt.Errorf("%w — if it's a permission problem, run with sudo", err)
			}

			fmt.Printf("Successfully upgraded astrona to %s!\nWhat's new: https://github.com/%s/releases/tag/%s\n", latestTag, releaseRepo, latestTag)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force upgrade even if already on the latest version")

	return cmd
}

// currentExecutable is the running astrona binary, symlinks resolved — a
// Homebrew install runs through <prefix>/bin/astrona → ../Cellar/….
func currentExecutable() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to resolve current executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	return execPath, nil
}

// isHomebrewInstall: exe lives in a Homebrew Cellar
// (<prefix>/Cellar/astrona/<version>/bin/astrona — /opt/homebrew,
// /usr/local or /home/linuxbrew/.linuxbrew), where brew, not
// `astrona upgrade`, manages it.
func isHomebrewInstall(exe string) bool {
	return strings.Contains(filepath.ToSlash(exe), "/Cellar/astrona/")
}

// upgradeCommand is how this install upgrades itself.
func upgradeCommand() string {
	if exe, err := currentExecutable(); err == nil && isHomebrewInstall(exe) {
		return "brew upgrade astrona"
	}
	return "astrona upgrade"
}
