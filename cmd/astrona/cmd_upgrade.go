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
		Use:          "upgrade",
		Short:        "Upgrade astrona-cli to the latest version",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to resolve current executable path: %w", err)
			}
			if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
				execPath = resolved
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
