package cluster

import (
	"fmt"
	"os"
	"os/exec"

	"astrona/internal/ui"
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

func CreateKindCluster(clusterName string, rep *ui.Reporter) error {
	t := rep.Step("Detect container engine")
	engine, err := DetectContainerEngine()
	if err != nil {
		return t.Fail(err)
	}

	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return t.Fail(fmt.Errorf("kind not found in PATH: %w", err))
	}
	t.Done()

	t = rep.Step("Create kind cluster %q (%s)", clusterName, engine.Name)

	cmd := exec.Command(kindPath, "create", "cluster", "--name", clusterName)
	out := t.Output()
	cmd.Stdout = out
	cmd.Stderr = out

	cmd.Env = os.Environ()
	if engine.Name == "podman" {
		cmd.Env = append(cmd.Env, "KIND_EXPERIMENTAL_PROVIDER=podman")
	}

	if err := cmd.Run(); err != nil {
		return t.Fail(err)
	}
	t.Done()
	return nil
}

func DeleteKindCluster(clusterName string, rep *ui.Reporter) error {
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return fmt.Errorf("kind not found in PATH: %w", err)
	}

	t := rep.Step("Delete kind cluster %q", clusterName)

	cmd := exec.Command(kindPath, "delete", "cluster", "--name", clusterName)
	out := t.Output()
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Run(); err != nil {
		return t.Fail(err)
	}
	t.Done()
	return nil
}
