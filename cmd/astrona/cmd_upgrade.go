package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
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
			"Installed with Homebrew? Then this runs `brew upgrade astrona` (`brew reinstall astrona` with " +
			"--force) instead of downloading the binary itself, so Homebrew keeps track of the install.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if exe, err := currentExecutable(); err == nil && isHomebrewInstall(exe) {
				return brewUpgrade(exe, force)
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

// runBrew runs a brew command; swapped out in tests.
var runBrew = (*exec.Cmd).Run

// brewUpgrade hands the upgrade to Homebrew, which owns the install:
// replacing the binary under its Cellar would leave brew believing the old
// version is still there. force reinstalls, like `astrona upgrade --force`.
func brewUpgrade(exe string, force bool) error {
	brew, err := brewBinary(exe)
	if err != nil {
		return err
	}
	verb := "upgrade"
	if force {
		verb = "reinstall"
	}
	fmt.Printf("astrona was installed with Homebrew — running: brew %s astrona\n", verb)
	cmd := exec.Command(brew, verb, "astrona")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := runBrew(cmd); err != nil {
		return fmt.Errorf("brew %s astrona failed: %w", verb, err)
	}
	return nil
}

// brewBinary is the brew that owns exe — <prefix>/bin/brew for an exe under
// <prefix>/Cellar/ — falling back to the brew on PATH.
func brewBinary(exe string) (string, error) {
	if prefix, _, ok := strings.Cut(filepath.ToSlash(exe), "/Cellar/"); ok {
		b := filepath.Join(filepath.FromSlash(prefix), "bin", "brew")
		if fi, err := os.Stat(b); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return b, nil
		}
	}
	if b, err := exec.LookPath("brew"); err == nil {
		return b, nil
	}
	return "", fmt.Errorf("astrona was installed with Homebrew (%s), but brew wasn't found — upgrade it with: brew upgrade astrona", exe)
}
