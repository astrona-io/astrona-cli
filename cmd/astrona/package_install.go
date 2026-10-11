package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// PackageFormat is set only in DEB, RPM and Snap builds. Bare binaries and
// Homebrew retain their existing tool lookup and upgrade behavior.
var PackageFormat string

func packageToolPath(executable, path, format string) string {
	if format != "deb" && format != "rpm" && format != "snap" {
		return path
	}
	tools := filepath.Join(filepath.Dir(executable), "tools")
	if path == "" {
		return tools
	}
	return tools + string(os.PathListSeparator) + path
}

func configurePackageTools() error {
	if PackageFormat == "" {
		return nil
	}
	exe, err := currentExecutable()
	if err != nil {
		return err
	}
	return os.Setenv("PATH", packageToolPath(exe, os.Getenv("PATH"), PackageFormat))
}

func packageUpgradeInstruction(format string) string {
	switch format {
	case "deb":
		return "sudo apt-get install ./astrona-<version>-linux-<arch>.deb"
	case "rpm":
		return "sudo dnf install ./astrona-<version>-linux-<arch>.rpm"
	case "snap":
		return "sudo snap install --dangerous --classic ./astrona-<version>-linux-<arch>.snap"
	default:
		return ""
	}
}

func packageUpgradeError(format string) error {
	return fmt.Errorf("astrona is managed by a %s package; download the newer package from https://github.com/%s/releases and install it with: %s (replace <version> and <arch> with the downloaded filename)", format, releaseRepo, packageUpgradeInstruction(format))
}
