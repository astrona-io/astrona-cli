package main

import (
	"fmt"
	"os"
	"os/exec"
)

// ApplyManifests runs `kubectl apply -f` for each manifest, always pinned
// to kubeContext explicitly rather than relying on whatever context is
// currently active.
func ApplyManifests(manifests []ResourceItem, baseDir, kubeContext string) error {
	if len(manifests) == 0 {
		return nil
	}

	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	for i, m := range manifests {
		if m.Source == "" {
			continue
		}

		path, err := resolveLocalSource(m, baseDir)
		if err != nil {
			return fmt.Errorf("failed to resolve manifest source for '%s': %w", m.Name, err)
		}

		fmt.Printf(" [%d/%d] Applying manifest: %s\n", i+1, len(manifests), m.Name)

		cmd := exec.Command(kubectlPath, "--context", kubeContext, "apply", "-f", path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to apply manifest '%s': %w", m.Name, err)
		}
	}

	return nil
}
