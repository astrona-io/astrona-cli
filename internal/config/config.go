package config

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"astrona/internal/gitsource"

	"gopkg.in/yaml.v3"
)

// maxConfigDownloadBytes bounds a lab config fetched from a URL — it's a
// small YAML document, so this cap is generous but still finite.
const maxConfigDownloadBytes = 10 * 1024 * 1024

// DocsConfig points at the markdown files that explain a lab: what you need
// to know before starting, the formal task, a softer version of the same
// task, and the full walkthrough.
type DocsConfig struct {
	Prerequisites string `yaml:"prerequisites"`
	ExamQuestion  string `yaml:"examQuestion"`
	CaseStudy     string `yaml:"caseStudy"`
	Guide         string `yaml:"guide"`
}

type MetadataConfig struct {
	Name string     `yaml:"name"`
	Docs DocsConfig `yaml:"docs"`
}

// RuntimeConfig picks which backend runs the lab. An empty/omitted Type
// means "kind" — every existing lab config with no runtime: block keeps
// working unchanged.
//
// QEMU is always a list — even a single-VM qemu lab is a one-element list,
// its one entry with no name set (see IsMultiVM/QEMUVM). "Extending" a
// single-VM lab into a multi-VM one is then just appending another list
// entry (with a name) rather than restructuring the whole block.
type RuntimeConfig struct {
	Type string   `yaml:"type"` // "" or "kind" (default) | "qemu"
	QEMU []QEMUVM `yaml:"qemu"`
	// Networks declares every named virtual network segment this lab's VMs
	// can join — see QEMUNetworkDef (hypervisor.go). Optional: a qemu lab
	// with no VM-to-VM networking needs no entry here.
	Networks []QEMUNetworkDef `yaml:"networks"`
}

// QEMUVM is one entry in runtime.qemu — either the lab's only VM (Name
// unset, the pre-existing single-VM shape every qemu lab used before
// multi-VM support) or one of several named VMs (IsMultiVM). AsQEMUConfig
// converts it to the QEMUConfig shape CreateQEMUVM/LoadQEMUHandle/
// DestroyQEMUVM (hypervisor.go) already expect — a multi-VM lab reuses that
// exact single-VM machinery per VM, under the synthesized name
// "<labName>-<vm.Name>", rather than a parallel code path.
type QEMUVM struct {
	Name       string          `yaml:"name"` // "" only valid when this is the list's only entry — see IsMultiVM
	Image      QEMUImageSource `yaml:"image"`
	Arch       string          `yaml:"arch"`
	CPUs       int             `yaml:"cpus"`
	MemoryMB   int             `yaml:"memoryMB"`
	DiskSizeGB int             `yaml:"diskSizeGB"`
	ExtraDisks []QEMUExtraDisk `yaml:"extraDisks"`
	// Networks attaches additional NICs to segments declared in the lab's
	// top-level runtime.networks — see QEMUNetwork (hypervisor.go). Every VM
	// always also gets an implicit host-only mgmt NIC regardless of this
	// list. Resolved lab-wide by resolveNetworkTopology, not by
	// AsQEMUConfig below — assigning a segment's listen/connect roles needs
	// to see every VM in the lab at once, not just this one.
	Networks []QEMUNetwork `yaml:"networks"`
	// SSHAccess names other VMs (by their runtime.qemu Name, from this same
	// list) this VM's ssh-user should be able to SSH into without a
	// password — e.g. a jump host reaching the hosts behind it.
	// hypervisor.ResolveInterVMTrust generates one dedicated ed25519
	// keypair per source VM (never the host's own per-VM access key, and
	// never written to the host's disk at all — the private half goes
	// straight into this VM's cloud-init seed, the public half into every
	// named target's), plus a ~/.ssh/config Host entry per target so `ssh
	// <target-vm-name>` just works from inside this VM. Both VMs must
	// already share a runtime.networks segment — sshAccess wires up trust
	// over a path that has to exist already, it doesn't create connectivity
	// of its own. Only meaningful once this is a multi-VM lab (IsMultiVM);
	// leave unset otherwise.
	SSHAccess       []string `yaml:"sshAccess"`
	SSHPort         int      `yaml:"sshPort"`
	Display         bool     `yaml:"display"`
	SSHPasswordAuth bool     `yaml:"sshPasswordAuth"`
	// Bootstrap and Validation, when set, run only against this one VM —
	// layered *after* the lab's shared root Bootstrap/Validation (LabConfig
	// fields), which for a multi-VM lab run against every VM in turn
	// instead of just once (see scripts.go's runBootstrap and proctor.go's
	// gradeScripts). Meaningless for a single-VM lab (this is the list's
	// only entry, root Bootstrap/Validation already cover it) — leave unset
	// there.
	Bootstrap  *BootstrapConfig  `yaml:"bootstrap"`
	Validation *ValidationConfig `yaml:"validation"`
}

func (vm QEMUVM) AsQEMUConfig() *QEMUConfig {
	return &QEMUConfig{
		Image:           vm.Image,
		Arch:            vm.Arch,
		CPUs:            vm.CPUs,
		MemoryMB:        vm.MemoryMB,
		DiskSizeGB:      vm.DiskSizeGB,
		ExtraDisks:      vm.ExtraDisks,
		SSHPort:         vm.SSHPort,
		Display:         vm.Display,
		SSHPasswordAuth: vm.SSHPasswordAuth,
	}
}

// IsMultiVM decides, from a lab's runtime.qemu list, whether this is a
// multi-VM lab (2+ entries, or a single entry that names itself) or a
// single-VM lab (exactly one entry with no name — the shape every qemu lab
// used before multi-VM support, and still the only shape that runs its
// root Bootstrap/Validation exactly once rather than once per VM).
func IsMultiVM(vms []QEMUVM) bool {
	if len(vms) != 1 {
		return true // 0 is invalid (ValidateQEMUVMs catches it), 2+ is unambiguous
	}
	return strings.TrimSpace(vms[0].Name) != ""
}

// ValidateQEMUVMs checks runtime.qemu is well-formed: not empty, and — only
// once it's actually multi-VM (IsMultiVM) — every entry named and no
// duplicate names. Called from every entry point that reads it
// (CreateEnvironment/LoadEnvironment/DestroyEnvironment in runtime.go) so a
// config mistake surfaces the same way regardless of which command catches
// it first.
func ValidateQEMUVMs(vms []QEMUVM) error {
	if len(vms) == 0 {
		return fmt.Errorf("runtime.qemu is empty — needs at least one entry")
	}
	if !IsMultiVM(vms) {
		return nil
	}

	seen := make(map[string]bool, len(vms))
	for _, vm := range vms {
		if strings.TrimSpace(vm.Name) == "" {
			return fmt.Errorf("every entry in runtime.qemu needs a name once there's more than one (or the one entry names itself)")
		}
		if seen[vm.Name] {
			return fmt.Errorf("duplicate vm name '%s' in runtime.qemu", vm.Name)
		}
		seen[vm.Name] = true
	}

	return nil
}

// ResourceItem is a single script or manifest reference used throughout the
// config: bootstrap/testing/teardown scripts, manifests to apply, and the
// optional validation script all share this same shape.
type ResourceItem struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Source      string `yaml:"source"`
}

type BootstrapConfig struct {
	Init      []ResourceItem `yaml:"init"`
	Manifests []ResourceItem `yaml:"manifests"`
}

type TeardownConfig struct {
	Init        []ResourceItem `yaml:"init"`
	KeepCluster bool           `yaml:"keepCluster"`
}

type ValidationCheck struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Resource string `yaml:"resource"`
	Command  string `yaml:"command"`
	Expect   string `yaml:"expect"`
}

type ValidationConfig struct {
	Checks []ValidationCheck `yaml:"checks"`
	Script *ResourceItem     `yaml:"script"`
	// Scripts (plural) runs alongside the singular Script above — additive,
	// not a replacement, so every existing lab's `script:` keeps working
	// unchanged. Exists for a multi-VM qemu lab, where one Script can only
	// ever target one VM (ResourceItem.VM): use Scripts to validate more
	// than one VM's state in a single `astrona submit`.
	Scripts []ResourceItem `yaml:"scripts"`
}

// LabConfig is the full shape of a lab's config.yaml.
type LabConfig struct {
	Metadata   MetadataConfig   `yaml:"metadata"`
	Runtime    RuntimeConfig    `yaml:"runtime"`
	Bootstrap  BootstrapConfig  `yaml:"bootstrap"`
	Testing    BootstrapConfig  `yaml:"testing"`
	Validation ValidationConfig `yaml:"validation"`
	Teardown   TeardownConfig   `yaml:"teardown"`
}

// ResolveConfigPath turns whatever the user passed via --config into an
// actual config file path. With gitURL set, configDirOrURL changes meaning:
// gitURL is cloned (or pulled, if already cached) via resolveGitConfigSource,
// and configDirOrURL becomes the subdirectory within that checkout to use
// (joined via joinWithinBaseDir, so it can't escape the clone — e.g. "." for
// the repo root, or "labs/lab-010" for a monorepo-style lab layout).
// Otherwise a direct URL is used as-is (or has the file name appended), a
// local directory gets the file name joined on, and a local file path is
// used as-is.
func ResolveConfigPath(configDirOrURL, fileName, gitURL, gitRef string, verbose bool) (string, error) {
	if gitURL != "" {
		repoDir, err := gitsource.ResolveGitConfigSource(gitURL, gitRef, verbose)
		if err != nil {
			return "", fmt.Errorf("git source failed: %w", err)
		}

		subDir, err := JoinWithinBaseDir(repoDir, configDirOrURL)
		if err != nil {
			return "", fmt.Errorf("--config must stay within the cloned repo: %w", err)
		}
		configDirOrURL = subDir
	}

	if strings.HasPrefix(configDirOrURL, "http://") || strings.HasPrefix(configDirOrURL, "https://") {
		if !strings.HasSuffix(configDirOrURL, ".yaml") && !strings.HasSuffix(configDirOrURL, ".yml") {
			configDirOrURL = strings.TrimSuffix(configDirOrURL, "/") + "/" + fileName
		}

		return configDirOrURL, nil
	}

	cleanPath := filepath.Clean(configDirOrURL)

	fileInfo, err := os.Stat(cleanPath)

	if err != nil {
		return "", fmt.Errorf("config path does not exist: %s", cleanPath)
	}

	if fileInfo.IsDir() {
		return filepath.Join(cleanPath, fileName), nil
	}

	return cleanPath, nil
}

// LoadLabConfig reads and parses a lab config, either from a local file or
// from an http(s) URL. The returned cleanup func is currently a no-op but
// keeps the signature symmetric with the other loader/downloader helpers.
func LoadLabConfig(configPath string) (*LabConfig, func(), error) {
	var body []byte
	cleanup := func() {}

	if strings.HasPrefix(configPath, "http://") || strings.HasPrefix(configPath, "https://") {
		if !strings.HasPrefix(configPath, "https://") {
			return nil, cleanup, fmt.Errorf("refusing to fetch lab config from non-https URL '%s': only https:// sources are allowed", configPath)
		}

		fmt.Printf("Fetching lab configuration from %s...\n", configPath)

		client := &http.Client{Timeout: 30 * time.Second}

		resp, err := client.Get(configPath)
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to fetch lab config: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, cleanup, fmt.Errorf("server returned status: %s", resp.Status)
		}

		b, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigDownloadBytes+1))
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to read response body: %w", err)
		}
		if len(b) > maxConfigDownloadBytes {
			return nil, cleanup, fmt.Errorf("lab config from '%s' exceeds %d byte limit", configPath, maxConfigDownloadBytes)
		}

		body = b
	} else {
		b, err := os.ReadFile(configPath)
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to read local config file: %w", err)
		}
		body = b
	}

	var config LabConfig
	err := yaml.Unmarshal(body, &config)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to parse lab YAML config: %w", err)
	}

	return &config, cleanup, nil
}

// NormalizeClusterName prefixes clusterName with "astro-" if it doesn't already
// start with "astro-".
func NormalizeClusterName(clusterName string) string {
	if clusterName == "" {
		clusterName = "astrona-lab"
	}
	if !strings.HasPrefix(clusterName, "astro-") {
		return "astro-" + clusterName
	}
	return clusterName
}

// NormalizeTestClusterName prefixes clusterName with "astro-test-" after stripping
// any existing "astro-" or "test-" prefixes to prevent nested naming.
func NormalizeTestClusterName(clusterName string) string {
	if clusterName == "" {
		clusterName = "astrona-lab"
	}
	clusterName = strings.TrimPrefix(clusterName, "astro-")
	clusterName = strings.TrimPrefix(clusterName, "test-")
	return "astro-test-" + clusterName
}
