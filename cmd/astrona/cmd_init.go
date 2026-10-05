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

	"github.com/spf13/cobra"
)

// labTemplates is the starter kind lab `astrona init lab` writes. all: is
// needed so .github/ is embedded too.
//
//go:embed all:templates/lab
var labTemplates embed.FS

const labTemplateRoot = "templates/lab"

var labNameRule = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,40}[a-z0-9])?$`)

// scaffoldLab renders every template into dir (which must not exist or be
// empty), with name as the lab name. Returns the files written, relative
// to dir.
func scaffoldLab(dir, name string) ([]string, error) {
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
	err = fs.WalkDir(labTemplates, labTemplateRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, labTemplateRoot+"/"), ".tmpl")
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
		if err := os.WriteFile(dest, out.Bytes(), 0644); err != nil {
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
	lab := &cobra.Command{
		Use:   "lab <dir>",
		Short: "Create a starter kind lab — config, docs, reference solution, CI workflow",
		Long: "Create a working kind lab in <dir> to build yours from: config.yaml (with editor " +
			"schema, preloaded image, readiness gates, jsonpath/count/resourceExists checks with " +
			"hints and points), the four student docs, a bootstrap manifest, a reference solution " +
			"for `astrona test`, and a GitHub Actions workflow running validate + test on every " +
			"pull request.\n\n" +
			"It passes `astrona validate` and `astrona test` as generated — change it from there.",
		Example: `  astrona init lab ./k8s-web-01
  astrona init lab ./labs/networking-01 --name net-01`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if name == "" {
				name = filepath.Base(filepath.Clean(dir))
			}
			files, err := scaffoldLab(dir, name)
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
	cmd.AddCommand(lab)
	return cmd
}
