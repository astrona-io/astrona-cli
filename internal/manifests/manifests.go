package manifests

import (
	"fmt"
	"os/exec"

	"astrona/internal/config"
	"astrona/internal/scripts"
	"astrona/internal/ui"
)

// ApplyManifests runs `kubectl apply -f` for each manifest, always pinned
// to kubeContext explicitly rather than relying on whatever context is
// currently active.
func ApplyManifests(manifests []config.ResourceItem, baseDir, kubeContext string, rep *ui.Reporter) error {
	if len(manifests) == 0 {
		return nil
	}

	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	for _, m := range manifests {
		if m.Source == "" {
			continue
		}

		t := rep.Step("Apply manifest: %s", m.Name)

		path, err := scripts.ResolveLocalSource(m, baseDir)
		if err != nil {
			return t.Fail(fmt.Errorf("failed to resolve manifest source for '%s': %w", m.Name, err))
		}

		cmd := exec.Command(kubectlPath, "--context", kubeContext, "apply", "-f", path)
		out := t.Output()
		cmd.Stdout = out
		cmd.Stderr = out

		if err := cmd.Run(); err != nil {
			return t.Fail(fmt.Errorf("failed to apply manifest '%s': %w", m.Name, err))
		}
		t.Done()
	}

	return nil
}
