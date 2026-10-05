package cluster

import (
	"fmt"
	"io"
	"os"
	"os/exec"

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
	if err := runTo(out, nil, engine.Path, "save", "-o", dest, img); err != nil {
		return fmt.Errorf("%s save failed: %w", engine.Name, err)
	}
	return nil
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
