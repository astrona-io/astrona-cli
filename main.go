package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)


type LabConfig struct {
	Name string
	ClusterName string
	ManifestURL string
}

// Validate checks if the LabConfig has valid values for required fields.
func (c *LabConfig) Validate() error {
	if c.Name == "" {
		return errors.New("Name is required")
	}
	if c.ClusterName == "" {
		return errors.New("ClusterName is required")
	}
	return nil
}


func main() {
	var clusterName string

	rootCmd := &cobra.Command{
		Use: "astrona",
		Short: "Astrona is a local Kubernetes lab manager",
		Long: `Astrona automates creating local kind clusters, deploying lab manifests, and validating resources.`,
	}

	upCmd := &cobra.Command{
		Use: "up",
		Short: "Spin up the lab Kubernetes cluster",
		RunE:  func(cmd *cobra.Command, args []string) error {
			err := CreateKindCluster(clusterName)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}

			fmt.Printf("Lab environment is ready")
			return nil
		},
	}

	downCmd := &cobra.Command{
		Use: "down",
		Short: "Spin up the lab Kubernetes cluster",
		RunE:  func(cmd *cobra.Command, args []string) error {
			err := DeleteKindCluster(clusterName)
			if err != nil {
				return fmt.Errorf("lab setup failed: %w", err)
			}

			fmt.Printf("Lab environment is ready")
			return nil
		},
	}

	upCmd.Flags().StringVarP(&clusterName, "name", "n", "astrona-lab", "name of the kind cluster")
	downCmd.Flags().StringVarP(&clusterName, "name", "n", "astrona-lab", "name of the kind cluster")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}