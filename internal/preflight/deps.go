package preflight

import "os/exec"

// DepCheck is one external dependency astrona shells out to. Required=false
// only ever produces a warning, never a non-zero exit — those tools back a
// specific runtime (qemu) or flag (--git), not astrona as a whole.
type DepCheck struct {
	Name        string
	Required    bool
	Note        string // why it's needed, or when it's optional
	Find        func() (found bool, detail string)
	InstallHint string
	BrewFormula string // what `brew install` installs it from; "" when Homebrew has nothing for it
}

// MissingDeps is every required (or every optional) check that fails.
func MissingDeps(required bool) []DepCheck {
	var out []DepCheck
	for _, c := range AstronaDepChecks() {
		if c.Required == required {
			if found, _ := c.Find(); !found {
				out = append(out, c)
			}
		}
	}
	return out
}

// lookPath is the DepCheck.Find for the common case: a single binary on
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

// AstronaDepChecks lists every external binary astrona shells out to,
// required ones first — `astrona check` relies on that ordering to print a
// "Required" section before an "Optional" one in a single pass.
func AstronaDepChecks() []DepCheck {
	return []DepCheck{
		{
			Name:        "kind",
			Required:    true,
			Note:        "creates and deletes the local Kubernetes cluster",
			Find:        lookPath("kind"),
			InstallHint: "https://kind.sigs.k8s.io/docs/user/quick-start/#installation",
			BrewFormula: "kind",
		},
		{
			Name:     "docker or podman",
			Required: true,
			Note:     "container runtime kind runs on (either one)",
			Find: func() (bool, string) {
				if path, err := exec.LookPath("docker"); err == nil {
					return true, "docker: " + path
				}
				if path, err := exec.LookPath("podman"); err == nil {
					return true, "podman: " + path
				}
				return false, ""
			},
			InstallHint: "https://docs.docker.com/get-docker/ or https://podman.io/docs/installation",
			BrewFormula: "podman",
		},
		{
			Name:        "kubectl",
			Required:    true,
			Note:        "applies manifests and runs the Proctor's checks",
			Find:        lookPath("kubectl"),
			InstallHint: "https://kubernetes.io/docs/tasks/tools/#kubectl",
			BrewFormula: "kubernetes-cli",
		},
		{
			Name:        "git",
			Required:    false,
			Note:        "only needed for --git (cloning/pulling a lab config from a repo)",
			Find:        lookPath("git"),
			InstallHint: "https://git-scm.com/downloads",
			BrewFormula: "git",
		},
		{
			Name:        "qemu-system-x86_64",
			Required:    false,
			Note:        "only needed for runtime.type: qemu with an x86_64 guest",
			Find:        lookPath("qemu-system-x86_64"),
			InstallHint: "brew install qemu (macOS) / apt install qemu-system-x86 (Debian/Ubuntu)",
			BrewFormula: "qemu",
		},
		{
			Name:        "qemu-system-aarch64",
			Required:    false,
			Note:        "only needed for runtime.type: qemu with an aarch64 guest",
			Find:        lookPath("qemu-system-aarch64"),
			InstallHint: "brew install qemu (macOS) / apt install qemu-system-arm (Debian/Ubuntu)",
			BrewFormula: "qemu",
		},
		{
			Name:        "qemu-img",
			Required:    false,
			Note:        "only needed for runtime.type: qemu (builds the disposable overlay disk)",
			Find:        lookPath("qemu-img"),
			InstallHint: "brew install qemu (macOS) / apt install qemu-utils (Debian/Ubuntu)",
			BrewFormula: "qemu",
		},
		{
			Name:        "ssh",
			Required:    false,
			Note:        "only needed for runtime.type: qemu (runs scripts inside the VM)",
			Find:        lookPath("ssh"),
			InstallHint: "usually preinstalled; on Debian/Ubuntu: apt install openssh-client",
		},
		{
			Name:        "ssh-keygen",
			Required:    false,
			Note:        "only needed for runtime.type: qemu (generates the VM's ephemeral SSH key)",
			Find:        lookPath("ssh-keygen"),
			InstallHint: "usually preinstalled; on Debian/Ubuntu: apt install openssh-client",
		},
		{
			Name:        "oras",
			Required:    false,
			Note:        "only needed for runtime.type: qemu with an image source of type 'oci' (pulls the base image from an OCI registry, e.g. ghcr.io)",
			Find:        lookPath("oras"),
			InstallHint: "https://oras.land/docs/installation",
			BrewFormula: "oras",
		},
		{
			Name:     "mkisofs / genisoimage / xorriso / hdiutil",
			Required: false,
			Note:     "only needed for runtime.type: qemu (builds the cloud-init seed image, any one of these works)",
			Find: func() (bool, string) {
				for _, tool := range []string{"mkisofs", "genisoimage", "xorriso", "hdiutil"} {
					if path, err := exec.LookPath(tool); err == nil {
						return true, tool + ": " + path
					}
				}
				return false, ""
			},
			InstallHint: "brew install cdrtools (macOS) / apt install genisoimage (Debian/Ubuntu)",
			BrewFormula: "cdrtools",
		},
	}
}
