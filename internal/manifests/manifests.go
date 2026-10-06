package manifests

import (
	"fmt"
	"os/exec"
	"strings"

	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/scripts"
	"astrona/internal/ui"
)

// MaxManifestDownloadBytes bounds a single downloaded `type: url` manifest.
const MaxManifestDownloadBytes = 50 * 1024 * 1024

// ApplyManifests runs `kubectl apply -f` for each manifest, always pinned
// to kubeContext explicitly rather than relying on whatever context is
// currently active.
func ApplyManifests(manifests []config.ResourceItem, baseDir, kubeContext string, rep *ui.Reporter) error {
	if len(manifests) == 0 {
		return nil
	}

	kubectlPath, err := executor.LookKubectl()
	if err != nil {
		return err
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

		// A URL manifest is downloaded with the https-only client and applied
		// from the temp file, rather than handed to `kubectl apply -f <url>`:
		// kubectl would follow an https→http redirect and has no size cap.
		cleanup := func() {}
		if strings.EqualFold(m.Type, "url") {
			path, cleanup, err = config.DownloadToTemp(path, "astrona-manifest-*.yaml", MaxManifestDownloadBytes)
			if err != nil {
				return t.Fail(fmt.Errorf("failed to download manifest from %s: %w", m.Source, err))
			}
		}

		cmd := exec.Command(kubectlPath, "--context", kubeContext, "apply", "-f", path)
		out := t.Output()
		cmd.Stdout = out
		cmd.Stderr = out

		err = cmd.Run()
		cleanup()
		if err != nil {
			return t.Fail(fmt.Errorf("failed to apply manifest '%s': %w", m.Name, err))
		}
		t.Done()
	}

	return nil
}
