package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
// DocsConfig is metadata.docs: the lab's documents, paths relative to
// config.yaml. A doc that isn't listed is found by its usual file name
// next to config.yaml (DocFileNames) — so a lab may leave docs out.
type DocsConfig struct {
	Question      string `yaml:"question"`
	Solution      string `yaml:"solution"`
	Prerequisites string `yaml:"prerequisites"`
	CaseStudy     string `yaml:"caseStudy"`
	// ExamQuestion and Guide are the older names of Question and
	// Solution; still read, folded into them by LoadLabConfig.
	ExamQuestion string `yaml:"examQuestion"`
	Guide        string `yaml:"guide"`
}

// DocFileNames are the file names a doc is found by when metadata.docs
// doesn't list it.
var DocFileNames = map[string]string{
	"question":      "question.md",
	"solution":      "solution.md",
	"prerequisites": "prerequisites.md",
	"caseStudy":     "case-study.md",
}

// resolve folds the older names into Question/Solution, then fills each
// doc not listed from its usual file in dir (the config's folder; "" for a
// config fetched from a URL, which has no folder to look in).
func (d *DocsConfig) resolve(dir string) error {
	for _, p := range []struct {
		name, old  string
		cur, older *string
	}{{"question", "examQuestion", &d.Question, &d.ExamQuestion}, {"solution", "guide", &d.Solution, &d.Guide}} {
		if *p.cur != "" && *p.older != "" && *p.cur != *p.older {
			return fmt.Errorf("metadata.docs: %s and %s are the same doc — keep %s", p.name, p.old, p.name)
		}
		if *p.cur == "" {
			*p.cur = *p.older
		}
		*p.older = ""
	}
	if dir == "" {
		return nil
	}
	for key, field := range map[string]*string{"question": &d.Question, "solution": &d.Solution, "prerequisites": &d.Prerequisites, "caseStudy": &d.CaseStudy} {
		if *field != "" {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(dir, DocFileNames[key])); err == nil && fi.Mode().IsRegular() {
			*field = DocFileNames[key]
		}
	}
	return nil
}

type MetadataConfig struct {
	Name string     `yaml:"name"`
	Docs DocsConfig `yaml:"docs"`
	// TimeLimit is how long a playground may run ("90m", "2h", "1h30m")
	// before its clock stops and its cluster is removed. Empty: the site's
	// default (2h). Only playgrounds started from the catalog are timed.
	TimeLimit string `yaml:"timeLimit"`
}

// MinTimeLimit and MaxTimeLimit bound metadata.timeLimit.
const (
	MinTimeLimit = 5 * time.Minute
	MaxTimeLimit = 24 * time.Hour
)

// TimeLimitMinutes is metadata.timeLimit in minutes (0: not set).
func (m MetadataConfig) TimeLimitMinutes() (int, error) {
	if strings.TrimSpace(m.TimeLimit) == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(m.TimeLimit))
	if err != nil {
		return 0, fmt.Errorf("metadata.timeLimit %q isn't a duration — write it like 90m, 2h or 1h30m", m.TimeLimit)
	}
	if d < MinTimeLimit || d > MaxTimeLimit {
		return 0, fmt.Errorf("metadata.timeLimit %s must be between %s and %s", d, MinTimeLimit, MaxTimeLimit)
	}
	return int(d.Minutes()), nil
}

// ValidateTimeLimit checks metadata.timeLimit before anything is built.
func ValidateTimeLimit(cfg *LabConfig) error {
	_, err := cfg.Metadata.TimeLimitMinutes()
	return err
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
	Type string `yaml:"type"` // "" or "kind" (default) | "qemu"
	// Kind shapes the kind cluster (node image, node count, networking) —
	// see KindConfig (kind.go). nil means kind's own defaults.
	Kind *KindConfig `yaml:"kind"`
	QEMU []QEMUVM    `yaml:"qemu"`
	// Networks declares every named virtual network segment this lab's VMs
	// can join — see QEMUNetworkDef (hypervisor.go). Optional: a qemu lab
	// with no VM-to-VM networking needs no entry here.
	Networks []QEMUNetworkDef `yaml:"networks"`
	// PortForwards are host-side `kubectl port-forward`s astrona keeps
	// running for a kind lab — see PortForward (portforward.go). kind only.
	PortForwards []PortForward `yaml:"portForwards"`
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
	// Hint and Points only apply when the item is a validation script —
	// see ValidationCheck.
	Hint   string `yaml:"hint"`
	Points int    `yaml:"points"`
}

type BootstrapConfig struct {
	Init      []ResourceItem `yaml:"init"`
	Manifests []ResourceItem `yaml:"manifests"`
	// WaitFor gates are run, in order, after Manifests — see WaitFor
	// (waitfor.go). kind only.
	WaitFor []WaitFor `yaml:"waitFor"`
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
	// Contains / ExpectRegex are looser matchers than Expect, for command,
	// jsonpath and http (body) checks; every matcher set must hold.
	Contains    string `yaml:"contains"`
	ExpectRegex string `yaml:"expectRegex"`
	// JSONPath is the kubectl JSONPath expression a jsonpath check reads
	// from Resource, e.g. "{.spec.replicas}".
	JSONPath string `yaml:"jsonpath"`
	// Min / Max bound how many objects a count check's Resource selects.
	Min *int `yaml:"min"`
	Max *int `yaml:"max"`
	// URL / ExpectStatus are for http checks (status default 200).
	URL          string `yaml:"url"`
	ExpectStatus int    `yaml:"expectStatus"`
	// Hint is shown to the student only when this check fails — a nudge,
	// not the answer.
	Hint string `yaml:"hint"`
	// Points weights the check in the score (default 1).
	Points int `yaml:"points"`
	// Cluster grades this check in a linked cluster instead of the lab's
	// own (the name of an entry in runtime.kind.clusters). Not for http checks.
	Cluster string `yaml:"cluster"`
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
	// PassPercent, when set, passes a submission once its score reaches
	// this percentage (e.g. 66, like a certification exam). 0 (default)
	// means every check and script must pass.
	PassPercent int `yaml:"passPercent"`
}

// LabConfig is the full shape of a lab's config.yaml.
type LabConfig struct {
	// APIVersion is the config format, "astrona.io/v1" (also when
	// omitted). A newer format is refused rather than half-understood.
	APIVersion string `yaml:"apiVersion"`
	// AstronaVersion is which astrona releases can run this lab, e.g.
	// "<=0.2.1" or ">=0.2.0, <0.3.0" (see internal/version). When this
	// binary isn't one of them, astrona hands the command to an installed
	// astrona-<version> that is (`astrona versions`).
	AstronaVersion string           `yaml:"astronaVersion"`
	Metadata       MetadataConfig   `yaml:"metadata"`
	Runtime        RuntimeConfig    `yaml:"runtime"`
	Bootstrap      BootstrapConfig  `yaml:"bootstrap"`
	Testing        BootstrapConfig  `yaml:"testing"`
	Validation     ValidationConfig `yaml:"validation"`
	Teardown       TeardownConfig   `yaml:"teardown"`
	// Exam turns the lab into a timed exam — see ExamConfig (exam.go).
	Exam ExamConfig `yaml:"exam"`
	// Resources describes files in the lab's resources/ folder, which a
	// student shows, copies or runs with `astrona resource` — see
	// LabResource (resources.go).
	Resources []LabResource `yaml:"resources"`

	// UnknownFields are keys no field reads (typos) — found by
	// LoadLabConfig, not part of the YAML. Lifecycle commands warn about
	// them; `astrona validate` / `astrona check` treat them as errors.
	// Deprecations are old spellings the config still uses (e.g.
	// runtime.kind.labs) — found by LoadLabConfig; warned about, still run.
	Deprecations  []string       `yaml:"-"`
	UnknownFields []UnknownField `yaml:"-"`

	// SourcePath / SourceSHA256 record where the config was loaded from
	// and the hash of its exact bytes (set by LoadLabConfig, not YAML) —
	// what a remote lab's trust approval is pinned to.
	SourcePath   string `yaml:"-"`
	SourceSHA256 string `yaml:"-"`
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
			return "", err
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
		return "", &NotFoundError{What: "lab", Path: cleanPath}
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

		fmt.Fprintf(os.Stderr, "Fetching lab configuration from %s...\n", configPath)

		client := HTTPSOnlyClient(30 * time.Second)

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
		if errors.Is(err, os.ErrNotExist) {
			return nil, cleanup, &NotFoundError{What: "lab config", Path: configPath}
		}
		if err != nil {
			return nil, cleanup, fmt.Errorf("can't read %s: %w", configPath, err)
		}
		body = b
	}

	var config LabConfig
	err := yaml.Unmarshal(body, &config)
	if err != nil {
		// A config for another astrona may not even parse here — still
		// say which astrona it wants, so the caller can hand over.
		var peek struct {
			AstronaVersion string `yaml:"astronaVersion"`
		}
		_ = yaml.Unmarshal(body, &peek)
		sum := sha256.Sum256(body)
		return nil, cleanup, &ParseError{Path: configPath, AstronaVersion: peek.AstronaVersion, SHA256: hex.EncodeToString(sum[:]), Err: err}
	}

	unknown, err := FindUnknownFields(body)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to parse lab YAML config: %w", err)
	}
	config.UnknownFields = unknown
	config.moveDeprecatedLabs()
	docsDir := ""
	if !strings.HasPrefix(configPath, "https://") {
		docsDir = filepath.Dir(configPath)
	}
	if err := config.Metadata.Docs.resolve(docsDir); err != nil {
		return nil, cleanup, fmt.Errorf("lab config %s: %w", configPath, err)
	}
	if err := config.ValidateNames(); err != nil {
		return nil, cleanup, fmt.Errorf("lab config %s: %w", configPath, err)
	}
	sum := sha256.Sum256(body)
	config.SourcePath = configPath
	config.SourceSHA256 = hex.EncodeToString(sum[:])

	return &config, cleanup, nil
}

// ValidateNames checks every config name that becomes a path component or
// part of one — metadata.name, runtime.qemu[].name and
// runtime.kind.clusters[].name — with ValidateName. LoadLabConfig runs it,
// so every command (destroy included) refuses such a config before
// touching the filesystem. Empty metadata/VM names are left to the code
// that defaults or requires them.
func (c *LabConfig) ValidateNames() error {
	if c.Metadata.Name != "" {
		if err := ValidateName(c.Metadata.Name); err != nil {
			return fmt.Errorf("metadata.name: %w", err)
		}
	}
	for i, vm := range c.Runtime.QEMU {
		if vm.Name == "" {
			continue
		}
		if err := ValidateName(vm.Name); err != nil {
			return fmt.Errorf("runtime.qemu[%d].name: %w", i, err)
		}
	}
	for i, l := range c.KindClusters() {
		if err := ValidateName(l.Name); err != nil {
			return fmt.Errorf("runtime.kind.clusters[%d].name: %w", i, err)
		}
	}
	return nil
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

// APIVersionV1 is the only config format this astrona understands.
const APIVersionV1 = "astrona.io/v1"

// ValidateAPIVersion refuses a config written for a newer format.
func ValidateAPIVersion(cfg *LabConfig) error {
	if cfg.APIVersion != "" && cfg.APIVersion != APIVersionV1 {
		return fmt.Errorf("apiVersion '%s' isn't supported by this astrona (it reads %s) — run `astrona upgrade`", cfg.APIVersion, APIVersionV1)
	}
	return nil
}

// ParseError is a lab config that failed to parse, with the astrona
// version it declares (if that much could be read) and the SHA-256 of
// its content — what a URL lab is pinned to for trust.
type ParseError struct {
	Path           string
	AstronaVersion string
	SHA256         string
	Err            error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s isn't valid YAML — %s", e.Path, strings.TrimPrefix(e.Err.Error(), "yaml: "))
}

// NotFoundError is a lab (directory) or lab config (file) that isn't
// there. It is an os.ErrNotExist.
type NotFoundError struct {
	What, Path string
}

func (e *NotFoundError) Error() string        { return fmt.Sprintf("no %s at %s", e.What, e.Path) }
func (e *NotFoundError) Is(target error) bool { return target == os.ErrNotExist }
func (e *ParseError) Unwrap() error           { return e.Err }
