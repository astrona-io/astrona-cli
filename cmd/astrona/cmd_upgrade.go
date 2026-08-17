package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

			if Version == latestTag && !force {
				fmt.Printf("You are already running the latest version (%s).\n", Version)
				return nil
			}

			fmt.Printf("Upgrading from %s to %s...\n", Version, latestTag)

			osName := runtime.GOOS
			archName := runtime.GOARCH

			assetName := fmt.Sprintf("astrona-%s-%s", osName, archName)
			downloadURL := fmt.Sprintf("https://github.com/astrona-io/astrona-cli/releases/download/%s/%s", latestTag, assetName)

			fmt.Printf("Downloading %s...\n", downloadURL)

			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to resolve current executable path: %w", err)
			}

			execDir := filepath.Dir(execPath)
			tempFile, err := os.CreateTemp(execDir, "astrona-upgrade-")
			if err != nil {
				return fmt.Errorf("failed to create temporary file: %w", err)
			}
			defer func() {
				tempFile.Close()
				os.Remove(tempFile.Name())
			}()

			assetResp, err := http.Get(downloadURL)
			if err != nil {
				return fmt.Errorf("failed to download release asset: %w", err)
			}
			defer assetResp.Body.Close()

			if assetResp.StatusCode != http.StatusOK {
				return fmt.Errorf("download failed with HTTP status: %s", assetResp.Status)
			}

			_, err = io.Copy(tempFile, assetResp.Body)
			if err != nil {
				return fmt.Errorf("failed to write download to temp file: %w", err)
			}

			err = os.Chmod(tempFile.Name(), 0755)
			if err != nil {
				return fmt.Errorf("failed to make binary executable: %w", err)
			}

			tempFile.Close()

			err = os.Rename(tempFile.Name(), execPath)
			if err != nil {
				return fmt.Errorf("failed to replace old binary: %w. Try running with sudo if you have permission issues", err)
			}

			fmt.Printf("Successfully upgraded astrona to %s!\n", latestTag)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force upgrade even if already on the latest version")

	return cmd
}
