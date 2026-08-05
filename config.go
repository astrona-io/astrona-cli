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
// actual config file path: a URL is used as-is (or has the file name
// appended), a directory gets the file name joined on, and a direct file
// path is used as-is.
func ResolveConfigPath(configDirOrURL, fileName string) (string, error) {
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
