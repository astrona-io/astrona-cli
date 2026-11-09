package main

import (
	"fmt"
	"path/filepath"

	"astrona/internal/authoring"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a new lab to start writing from",
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
			scaffoldFn := authoring.Scaffold
			if linked {
				scaffoldFn = authoring.ScaffoldLinked
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
