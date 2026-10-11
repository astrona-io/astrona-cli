package cluster

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"astrona/internal/ui"
)

// PreloadImages makes each image available inside every node of
// clusterName without the node pulling it: pulled on the host only if the
// container engine doesn't already have it (so repeat runs need no
// network), saved to a private temp archive, and loaded with `kind load
// image-archive` — the same path for Docker and Podman.
//
// Images are expected to be validated already (config.ValidateKindConfig):
// explicit tag or digest, never :latest.
func PreloadImages(clusterName string, images []string, rep *ui.Reporter) error {
	if len(images) == 0 {
		return nil
	}
	engine, err := DetectContainerEngine()
	if err != nil {
		return err
	}
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return fmt.Errorf("kind not found in PATH: %w", err)
	}

	for _, img := range images {
		t := rep.Step("Preload image %s", img)
		if err := preloadOne(engine, kindPath, clusterName, img, t.Output()); err != nil {
			return t.Fail(fmt.Errorf("preload '%s': %w", img, err))
		}
		t.Done()
	}
	return nil
}

func preloadOne(engine ContainerEngine, kindPath, clusterName, img string, out io.Writer) error {
	archive, err := os.CreateTemp("", "astrona-image-*.tar")
	if err != nil {
		return fmt.Errorf("create image archive: %w", err)
	}
	archive.Close()
	defer os.Remove(archive.Name())

	if err := saveImage(engine, img, archive.Name(), out); err != nil {
		return err
	}
	if err := runTo(out, kindEnv(), kindPath, "load", "image-archive", archive.Name(), "--name", clusterName); err != nil {
		return fmt.Errorf("kind load failed: %w", err)
	}
	return nil
}

func runTo(out io.Writer, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// saveImage writes img to dest as a docker-archive, pulling it first only
// if the engine doesn't already have it.
func saveImage(engine ContainerEngine, img, dest string, out io.Writer) error {
	if engine.Name == "podman" {
		// Lab image names follow Docker's Docker Hub shorthand. Podman on
		// Ubuntu has no default search registry and cannot resolve these
		// noninteractively. Qualify them without changing host configuration.
		img = dockerHubReference(img)
	}
	// Just a presence check — "image not known" is the expected answer
	// for a missing image, not something to show.
	if err := exec.Command(engine.Path, "image", "inspect", "--format", "{{.Id}}", img).Run(); err != nil {
		fmt.Fprintf(out, "%s not in the local %s cache, pulling\n", img, engine.Name)
		if err := runTo(out, nil, engine.Path, "pull", img); err != nil {
			return fmt.Errorf("%s pull failed: %w", engine.Name, err)
		}
	} else {
		fmt.Fprintf(out, "using %s from the local %s cache\n", img, engine.Name)
	}
	args := []string{"save", "-o", dest}
	// Docker's containerd store can export an index whose other platforms
	// aren't cached. Kind imports all platforms and fails on the missing
	// blobs. Export only the daemon's platform when the CLI supports it.
	if engine.Name == "docker" {
		help, err := exec.Command(engine.Path, "save", "--help").Output()
		if err != nil {
			return fmt.Errorf("docker save capabilities: %w", err)
		}
		if strings.Contains(string(help), "--platform") {
			platform, err := exec.Command(engine.Path, "version", "--format", "{{.Server.APIVersion}} {{.Server.Os}}/{{.Server.Arch}}").Output()
			if err != nil {
				return fmt.Errorf("docker server platform: %w", err)
			}
			fields := strings.Fields(string(platform))
			var major, minor int
			if len(fields) != 2 {
				return fmt.Errorf("invalid Docker server platform response %q", strings.TrimSpace(string(platform)))
			}
			if _, err := fmt.Sscanf(fields[0], "%d.%d", &major, &minor); err != nil {
				return fmt.Errorf("invalid Docker server API version: %w", err)
			}
			p := fields[1]
			if p != "linux/amd64" && p != "linux/arm64" {
				return fmt.Errorf("unsupported Docker platform %q for kind", p)
			}
			// --platform requires daemon API 1.48 (Docker 28) or newer.
			if major > 1 || (major == 1 && minor >= 48) {
				args = append(args, "--platform", p)
			}
		}
	}
	args = append(args, img)
	if err := runTo(out, nil, engine.Path, args...); err != nil {
		return fmt.Errorf("%s save failed: %w", engine.Name, err)
	}
	return nil
}

func dockerHubReference(img string) string {
	first, _, slash := strings.Cut(img, "/")
	if !slash {
		return "docker.io/library/" + img
	}
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return img
	}
	return "docker.io/" + img
}

// SaveImage writes img (pulled if missing) to dest as an archive the
// engine can `load` — used to build offline bundles.
func SaveImage(img, dest string, out io.Writer) error {
	engine, err := DetectContainerEngine()
	if err != nil {
		return err
	}
	return saveImage(engine, img, dest, out)
}

// LoadImageArchive loads an image archive into the host's container
// engine, so kind and preloading find it without a registry.
func LoadImageArchive(archive string, out io.Writer) error {
	engine, err := DetectContainerEngine()
	if err != nil {
		return err
	}
	if err := runTo(out, nil, engine.Path, "load", "-i", archive); err != nil {
		return fmt.Errorf("%s load failed: %w", engine.Name, err)
	}
	return nil
}
