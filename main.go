package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func ResolveConfigPath(configDirOrURL, fileName string) (string, error) {
	if strings.HasPrefix(configDirOrURL, "http://") || strings.HasPrefix(configDirOrURL, "https://") {
		if !strings.HasSuffix(configDirOrURL, ".yaml") && !strings.HasSuffix(configDirOrURL, ".yml") {
			configDirOrURL = strings.TrimSuffix(configDirOrURL, "/") +"/"+ fileName
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

func main() {
	var configPath string
	var fileName string

	rootCmd := &cobra.Command{
		Use: "astrona",
		Short: "Astrona is a local Kubernetes lab manager",
		Long: `Astrona automates creating local kind clusters, deploying lab manifests, and validating resources.`,
	}

	upCmd := &cobra.Command{
		Use: "up",
		Short: "Spin up the lab Kubernetes cluster",
		RunE:  func(cmd *cobra.Command, args []string) error {
			finalPath, err := ResolveConfigPath(configPath, fileName)
			if err != nil {
				return fmt.Errorf("path resolution failed: %w", err)
			}

			fmt.Printf("Loading configuration from: %s\n", err)

			if configPath == "" {
				return fmt.Errorf("please specify a configuration file using --config or -c")
			}

			config, configCleanup, err := LoadLabConfig(finalPath)
			if err != nil {
				return fmt.Errorf("failed to load lab config: %w", err)
			}
			defer configCleanup()

			fmt.Printf("Initializing Lab: %s\n", config.Metadata.Name)

			clusterName := config.Metadata.Name
			if clusterName == "" {
				clusterName = "astrona-lab"
			}

			err = CreateKindCluster(clusterName)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}
			fmt.Printf("Lab environment is ready")

			if len(config.Bootstrap.Init) > 0 {
				fmt.Printf("Running bootstrap init scripts...\n")
				if err := RunInitScripts(config.Bootstrap.Init); err != nil {
					return fmt.Errorf("init scripts failed: %w", err)
				}
			}

			if len(config.Bootstrap.Manifests) > 0 {
				fmt.Printf("Applying bootstrap manifests...\n")
			}

			fmt.Printf("\nLab environment is fully loaded and ready!")
			return nil
		},
	}

	downCmd := &cobra.Command{
		Use: "down",
		Short: "Spin up the lab Kubernetes cluster",
		RunE:  func(cmd *cobra.Command, args []string) error {
			finalPath, err := ResolveConfigPath(configPath, fileName)
			if err != nil {
				return fmt.Errorf("path resolution failed: %w", err)
			}

			fmt.Printf("Loading configuration from: %s\n", err)

			clusterName := "astrona-lab"

			if finalPath != "" {
				config, _, err := LoadLabConfig(finalPath)
				if err == nil && config.Metadata.Name != "" {
					clusterName = config.Metadata.Name
				}
			}

			err = DeleteKindCluster(clusterName)
			if err != nil {
				return fmt.Errorf("lab teardown failed: %w", err)
			}

			fmt.Printf("Lab cluster cleaned up successfully.")
			return nil
		},
	}

	upCmd.Flags().StringVarP(&configPath, "config", "c", ".", "Path or URL to the lab configuration YAML file")
	upCmd.Flags().StringVarP(&fileName, "file", "f", "config.yaml", "Configuration file name override")
	downCmd.Flags().StringVarP(&configPath, "config", "c", ".", "Path or URL to the lab configuration YAML file")
	downCmd.Flags().StringVarP(&fileName, "file", "f", "config.yaml", "Configuration file name override")


	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}