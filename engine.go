package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type MetadataConfig struct {
	Name string `yaml:"name"`
}

type ResourceItem struct {
	Name string `yaml:"name"`
	Description string `yaml:"description"`
	Type string `yaml:"type"`
	Source string `yaml:"source"`
}

type BootstrapConfig struct {
	Init 			[]ResourceItem `yaml:"init"`
	Manifests 		[]ResourceItem `yaml:"manifests"`
}

type LabConfig struct {
	Metadata		MetadataConfig `yaml:"metadata"`
	Bootstrap		BootstrapConfig `yaml:"bootstrap"`
}

type ContainerEngine struct {
	Name string
	Path string
}


func DetectContainerEngine() (ContainerEngine, error) {
	// Check for Docker
	if path, err := exec.LookPath("docker"); err == nil {
		return ContainerEngine{Name: "docker", Path: path}, nil
	}

	// Check for Podman
	if path, err := exec.LookPath("podman"); err == nil {
		return ContainerEngine{Name: "podman", Path: path}, nil
	}

	return ContainerEngine{}, fmt.Errorf("no container engine found PATH")
}

func CreateKindCluster(clusterName string) error {
	engine, err := DetectContainerEngine()
	if err != nil {
		return err
	}

	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return fmt.Errorf("kind not found in PATH: %v", err)
	}

	fmt.Printf("Creating kind cluster '%s' using %s...\n", clusterName, engine.Name)

	cmd := exec.Command(kindPath, "create", "cluster", "--name", clusterName)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KIND_EXPERIMENTAL_PROVIDER=%s", engine.Name))

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = os.Environ()

	if engine.Name == "podman" {
		cmd.Env = append(cmd.Env, "KIND_EXPERIMENTAL_PROVIDER=podman")
	}

	return cmd.Run()
}

func DeleteKindCluster(clusterName string) error {
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return fmt.Errorf("kind not found in PATH: %v", err)
	}

	fmt.Printf("Deleting kind cluster '%s'...\n", clusterName)

	cmd := exec.Command(kindPath, "delete", "cluster", "--name", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func LoadLabConfig(configPath string) (*LabConfig, func(), error) {
	var body []byte
	cleanup := func ()  {}

	if strings.HasPrefix(configPath, "http://") || strings.HasPrefix(configPath, "https://") {
		fmt.Printf("Fetching lab configuration from %s...\n", configPath)

		resp, err := http.Get(configPath)
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to fetch lab config: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, cleanup, fmt.Errorf("server returned status: %s", resp.Status)
		}

		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to read response body: %w", err)
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

func RunInitScripts(scripts []ResourceItem, baseDir string) error {
	for i, s := range scripts {
		if s.Source == "" {
			continue
		}

		fmt.Printf(" [%d/%d] Init: %s\n", i+1, len(scripts), s.Name)
		if s.Description != "" {
			fmt.Printf("[INFO] %s\n", s.Description)
		}

		scriptPath := s.Source
		var cleanup func() = func() {}

		switch strings.ToLower(s.Type) {
		case "url":
			tmpPath, clean, err := downloadToTemp(s.Source, "astrona-script-*.sh")
			if err != nil {
				return fmt.Errorf("failed to download script from %s: %w", s.Source, err)
			}

			scriptPath = tmpPath
			cleanup = clean
		case "file":
			if !filepath.IsAbs(s.Source) {
				scriptPath = filepath.Join(baseDir, s.Source)
			}

			if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
				return fmt.Errorf("local script file does not exist %s: %w", scriptPath, err)
			}
		default:
			return fmt.Errorf("unsupported type '%s' for init scripts '%s' (must be 'file' or 'folder')", s.Type, s.Name)
		}

		cmd := exec.Command("bash", scriptPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Run()
		cleanup()

		if err != nil {
			return fmt.Errorf("failed to run script '%s': %s", s.Name, err)
		}
	}

	return nil
}

func downloadToTemp(url, filePattern string) (string, func(), error) {
	cleanup := func() {}

	resp, err := http.Get(url)
	if err != nil {
		return "", cleanup, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", cleanup, fmt.Errorf("server returned status: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", filePattern)
	if err != nil {
		return "", cleanup, err
	}

	cleanup = func() {
		os.Remove(tmpFile.Name())
	}

	_, err = io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		cleanup()
		return "", cleanup, err
	}

	os.Chmod(tmpFile.Name(), 0755)

	return tmpFile.Name(), cleanup, nil
}