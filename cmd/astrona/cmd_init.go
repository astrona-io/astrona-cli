package main

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

	"github.com/spf13/cobra"
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

// scaffoldLab renders the starter lab into dir (which must not exist or be
// empty), with name as the lab name. Returns the files written, relative
// to dir.
func scaffoldLab(dir, name string) ([]string, error) {
	return scaffold(dir, name, labTemplateRoot)
}

// scaffoldLinkedLab renders the starter lab with a linked "backend"
// cluster (runtime.kind.clusters) into dir.
func scaffoldLinkedLab(dir, name string) ([]string, error) {
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

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold new astrona content",
	}

	var name string
	var linked bool
	lab := &cobra.Command{
		Use:   "lab <dir>",
		Short: "Create a starter kind lab — config, docs, reference solution, CI workflow",
		Long: "Create a working kind lab in <dir> to build yours from: config.yaml (with editor " +
			"schema, preloaded image, readiness gates, jsonpath/count/resourceExists checks with " +
			"hints and points), the four student docs, a bootstrap manifest, a reference solution " +
			"for `astrona test`, and a GitHub Actions workflow running validate + test on every " +
			"pull request.\n\n" +
			"--linked adds a second kind cluster running side by side (runtime.kind.clusters): a " +
			"backend service published as a NodePort, a task that reaches it from the lab's " +
			"cluster, and a check graded in the backend cluster — see the Linked Labs guide.\n\n" +
			"It passes `astrona validate` and `astrona test` as generated — change it from there.",
		Example: `  astrona init lab ./k8s-web-01
  astrona init lab ./labs/networking-01 --name net-01
  astrona init lab ./auth-01 --linked`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if name == "" {
				name = filepath.Base(filepath.Clean(dir))
			}
			scaffoldFn := scaffoldLab
			if linked {
				scaffoldFn = scaffoldLinkedLab
			}
			files, err := scaffoldFn(dir, name)
			if err != nil {
				return err
			}
			fmt.Printf("Created lab %s in %s:\n", name, dir)
			for _, f := range files {
				fmt.Printf("  %s\n", f)
			}
			fmt.Printf("\nNext:\n  astrona validate -c %s\n  astrona test -c %s       # proves solution/ passes every check\n  astrona run -c %s\n", dir, dir, dir)
			return nil
		},
	}
	lab.Flags().StringVar(&name, "name", "", "Lab name (metadata.name); default: the directory name")
	lab.Flags().BoolVar(&linked, "linked", false, "Add a second kind cluster (\"backend\") running side by side, with a task and checks across both")
	cmd.AddCommand(lab)
	return cmd
}
