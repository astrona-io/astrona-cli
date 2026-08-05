package main

import (
	"fmt"
	"os"
	"os/exec"
)

// ContainerEngine is whichever of Docker/Podman was found on PATH — kind
// needs to know which one to drive the local cluster with.
type ContainerEngine struct {
	Name string
	Path string
}

func DetectContainerEngine() (ContainerEngine, error) {
	if path, err := exec.LookPath("docker"); err == nil {
		return ContainerEngine{Name: "docker", Path: path}, nil
	}

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
		return fmt.Errorf("kind not found in PATH: %w", err)
	}

	fmt.Printf("Creating kind cluster '%s' using %s...\n", clusterName, engine.Name)

	cmd := exec.Command(kindPath, "create", "cluster", "--name", clusterName)
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
		return fmt.Errorf("kind not found in PATH: %w", err)
	}

	fmt.Printf("Deleting kind cluster '%s'...\n", clusterName)

	cmd := exec.Command(kindPath, "delete", "cluster", "--name", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
