package main

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

// depCheck is one external dependency astrona shells out to. required=false
// only ever produces a warning, never a non-zero exit — those tools back a
// specific runtime (qemu) or flag (--git), not astrona as a whole.
type depCheck struct {
	name        string
	required    bool
	note        string // why it's needed, or when it's optional
	find        func() (found bool, detail string)
	installHint string
}

// lookPath is the depCheck.find for the common case: a single binary on
// PATH.
func lookPath(bin string) func() (bool, string) {
	return func() (bool, string) {
		path, err := exec.LookPath(bin)
		if err != nil {
			return false, ""
		}
		return true, path
	}
}

// astronaDepChecks lists every external binary astrona shells out to,
// required ones first — newCheckCmd relies on that ordering to print a
// "Required" section before an "Optional" one in a single pass.
func astronaDepChecks() []depCheck {
	return []depCheck{
		{
			name:        "kind",
			required:    true,
			note:        "creates and deletes the local Kubernetes cluster",
			find:        lookPath("kind"),
			installHint: "https://kind.sigs.k8s.io/docs/user/quick-start/#installation",
		},
		{
			name:     "docker or podman",
			required: true,
			note:     "container runtime kind runs on (either one)",
			find: func() (bool, string) {
				if path, err := exec.LookPath("docker"); err == nil {
					return true, "docker: " + path
				}
				if path, err := exec.LookPath("podman"); err == nil {
					return true, "podman: " + path
				}
				return false, ""
			},
			installHint: "https://docs.docker.com/get-docker/ or https://podman.io/docs/installation",
		},
		{
			name:        "kubectl",
			required:    true,
			note:        "applies manifests and runs the Proctor's checks",
			find:        lookPath("kubectl"),
			installHint: "https://kubernetes.io/docs/tasks/tools/#kubectl",
		},
		{
			name:        "git",
			required:    false,
			note:        "only needed for --git (cloning/pulling a lab config from a repo)",
			find:        lookPath("git"),
			installHint: "https://git-scm.com/downloads",
		},
		{
			name:        "qemu-system-x86_64",
			required:    false,
			note:        "only needed for runtime.type: qemu with an x86_64 guest",
			find:        lookPath("qemu-system-x86_64"),
			installHint: "brew install qemu (macOS) / apt install qemu-system-x86 (Debian/Ubuntu)",
		},
		{
			name:        "qemu-system-aarch64",
			required:    false,
			note:        "only needed for runtime.type: qemu with an aarch64 guest",
			find:        lookPath("qemu-system-aarch64"),
			installHint: "brew install qemu (macOS) / apt install qemu-system-arm (Debian/Ubuntu)",
		},
		{
			name:        "qemu-img",
			required:    false,
			note:        "only needed for runtime.type: qemu (builds the disposable overlay disk)",
			find:        lookPath("qemu-img"),
			installHint: "brew install qemu (macOS) / apt install qemu-utils (Debian/Ubuntu)",
		},
		{
			name:        "ssh",
			required:    false,
			note:        "only needed for runtime.type: qemu (runs scripts inside the VM)",
			find:        lookPath("ssh"),
			installHint: "usually preinstalled; on Debian/Ubuntu: apt install openssh-client",
		},
		{
			name:        "ssh-keygen",
			required:    false,
			note:        "only needed for runtime.type: qemu (generates the VM's ephemeral SSH key)",
			find:        lookPath("ssh-keygen"),
			installHint: "usually preinstalled; on Debian/Ubuntu: apt install openssh-client",
		},
		{
			name:        "oras",
			required:    false,
			note:        "only needed for runtime.type: qemu with an image source of type 'oci' (pulls the base image from an OCI registry, e.g. ghcr.io)",
			find:        lookPath("oras"),
			installHint: "https://oras.land/docs/installation",
		},
		{
			name:     "mkisofs / genisoimage / xorriso / hdiutil",
			required: false,
			note:     "only needed for runtime.type: qemu (builds the cloud-init seed image, any one of these works)",
			find: func() (bool, string) {
				for _, tool := range []string{"mkisofs", "genisoimage", "xorriso", "hdiutil"} {
					if path, err := exec.LookPath(tool); err == nil {
						return true, tool + ": " + path
					}
				}
				return false, ""
			},
			installHint: "brew install cdrtools (macOS) / apt install genisoimage (Debian/Ubuntu)",
		},
	}
}

// newCheckCmd builds `astrona check`: verifies every external binary
// astrona shells out to is on PATH, printing a colored ✓/⚠/✗ per
// dependency. A missing required dependency (the default kind runtime's
// own toolchain) is a non-zero exit; a missing optional one (qemu, git) is
// a warning only, since it's only needed for a specific runtime or flag.
func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "check",
		Short:        "Check that astrona's dependencies are installed",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := astronaDepChecks()

			missingRequired := 0
			printedOptionalHeader := false

			fmt.Printf("astrona version: %s\n\n", Version)

			fmt.Println("Required:")
			for _, c := range checks {
				if !c.required && !printedOptionalHeader {
					fmt.Println("\nOptional (only needed for specific runtimes/flags):")
					printedOptionalHeader = true
				}

				found, detail := c.find()
				if found {
					fmt.Printf("  %s  %-38s %s\n", colorize(ansiGreen, "✓"), c.name, detail)
					continue
				}

				if c.required {
					missingRequired++
					fmt.Printf("  %s  %-38s %s\n", colorize(ansiRed, "✗"), c.name, c.note)
				} else {
					fmt.Printf("  %s  %-38s %s\n", colorize(ansiYellow, "⚠"), c.name, c.note)
				}
				fmt.Printf("        install: %s\n", c.installHint)
			}

			fmt.Println()
			if missingRequired > 0 {
				return fmt.Errorf("%d required dependency(ies) missing (see ✗ above) — astrona cannot run until these are installed", missingRequired)
			}

			fmt.Println("All required dependencies found. Optional ⚠ warnings above only matter if you use that runtime/flag.")
			return nil
		},
	}
}
