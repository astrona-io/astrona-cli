// Package authoring holds the lab-author workflows behind `astrona init`
// and friends, free of Cobra so other front ends can call them. The
// commands, flags and printing stay in cmd/astrona.
//
// `astrona validate` is CheckConfig (a preflight.ConfigChecker, which cmd
// also hands to check, doctor and run) wrapped by ValidateConfig into a
// ValidateReport. `astrona bundle` is PrepareBundle/CreateBundle (a
// BundleResult), OpenBundle/Install for load, and InspectBundle; the trust
// prompt for a loaded bundle and all printing stay in cmd. Results and
// errors are returned, never printed: progress goes to the *ui.Reporter or
// io.Writer the caller passes.
//
// RunTest (devtest.go) is one `astrona test` lifecycle; it writes the
// grading report only to the writer it is given.
package authoring

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"astrona/internal/config"
)

// labTemplates are the starters `astrona init lab` writes: a kind lab,
// and (--linked) a kind lab with a linked cluster. all: is needed so
// .github/ is embedded too.
//
//go:embed all:templates/lab all:templates/linked
var labTemplates embed.FS

const (
	labTemplateRoot    = "templates/lab"
	linkedTemplateRoot = "templates/linked"
)

var labNameRule = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,40}[a-z0-9])?$`)

// Scaffold renders the starter lab into dir (which must not exist or be
// empty), with name as the lab name. Returns the files written, relative
// to dir.
func Scaffold(dir, name string) ([]string, error) {
	return scaffold(dir, name, labTemplateRoot)
}

// ScaffoldLinked renders the starter lab with a linked "backend"
// cluster (runtime.kind.clusters) into dir.
func ScaffoldLinked(dir, name string) ([]string, error) {
	// Checked before writing anything: the backend's node name must fit
	// what `astrona run` accepts (see config.ValidateKindClusters).
	probe := &config.LabConfig{
		Metadata: config.MetadataConfig{Name: name},
		Runtime:  config.RuntimeConfig{Kind: &config.KindConfig{Clusters: []config.KindCluster{{Name: "backend"}}}},
	}
	if err := config.ValidateKindClusters(probe); err != nil {
		return nil, fmt.Errorf("lab name '%s' is too long for a lab with a linked cluster: %w", name, err)
	}
	return scaffold(dir, name, linkedTemplateRoot)
}

func scaffold(dir, name, root string) ([]string, error) {
	if !labNameRule.MatchString(name) {
		return nil, fmt.Errorf("lab name '%s' must be lowercase letters, digits and '-' (max 42), e.g. 'k8s-web-01'", name)
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s already exists and isn't empty — pick a new directory", dir)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	data := struct{ Name string }{Name: name}
	var written []string
	err = fs.WalkDir(labTemplates, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl")
		src, err := labTemplates.ReadFile(p)
		if err != nil {
			return err
		}
		// [[ ]] delimiters leave GitHub Actions' ${{ }} and kubectl
		// JSONPath braces alone.
		tmpl, err := template.New(rel).Delims("[[", "]]").Option("missingkey=error").Parse(string(src))
		if err != nil {
			return fmt.Errorf("template %s: %w", rel, err)
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, data); err != nil {
			return fmt.Errorf("template %s: %w", rel, err)
		}
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0755
		}
		if err := os.WriteFile(dest, out.Bytes(), mode); err != nil {
			return err
		}
		written = append(written, path.Clean(rel))
		return nil
	})
	return written, err
}
