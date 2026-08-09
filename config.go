package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
// working unchanged. QEMU is defined in hypervisor.go, alongside the rest of
// the qemu-specific logic it configures.
type RuntimeConfig struct {
	Type string      `yaml:"type"` // "" or "kind" (default) | "qemu"
	QEMU *QEMUConfig `yaml:"qemu"`
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
func ResolveConfigPath(configDirOrURL, fileName, gitURL, gitRef string) (string, error) {
	if gitURL != "" {
		repoDir, err := resolveGitConfigSource(gitURL, gitRef)
		if err != nil {
			return "", fmt.Errorf("git source failed: %w", err)
		}

		subDir, err := joinWithinBaseDir(repoDir, configDirOrURL)
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

// LoadLabForCommand resolves --config/--file (and --git/--git-ref, if set),
// loads the YAML, and returns the lab's base directory for resolving
// relative script/manifest paths. Shared by every command that requires a
// valid config to do anything (run, submit, test) — cmd_destroy.go does NOT
// use this, since destroy must still best-effort tear down even when the
// config can't be loaded.
func LoadLabForCommand(flags *rootFlags) (config *LabConfig, baseDir string, cleanup func(), err error) {
	finalPath, err := ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef)
	if err != nil {
		return nil, "", func() {}, fmt.Errorf("path resolution failed: %w", err)
	}

	fmt.Printf("Loading configuration from: %s\n", finalPath)

	config, cleanup, err = LoadLabConfig(finalPath)
	if err != nil {
		return nil, "", func() {}, fmt.Errorf("failed to load lab config: %w", err)
	}

	return config, filepath.Dir(finalPath), cleanup, nil
}
