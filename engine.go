package main

import (
	"fmt"
	"os"
	"os/exec"
)

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