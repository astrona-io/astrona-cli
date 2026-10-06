package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every command a user sees in `astrona --help` belongs to a group — a new
// command added without one would land in "Additional Commands".
func TestEveryVisibleCommandIsGrouped(t *testing.T) {
	root := newRootCmd(&rootFlags{})
	ungrouped := map[string]bool{"upgrade": true, "help": true, "completion": true}
	for _, c := range root.Commands() {
		if c.Hidden || ungrouped[c.Name()] {
			continue
		}
		if c.GroupID == "" {
			t.Errorf("command %q has no help group — add it to commandGroups", c.Name())
		}
	}

	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	last := -1
	for _, title := range []string{"Take a lab:", "Manage running labs:", "Write labs", "Troubleshoot:", "Additional Commands:"} {
		i := strings.Index(help, title)
		if i < last {
			t.Errorf("%q missing or out of order in help", title)
		}
		last = i
	}
	if strings.Index(help, "  run ") > strings.Index(help, "  submit ") {
		t.Error("run should be listed before submit (workflow order)")
	}
}

// Every command's CLI reference page is in the docs menu (mkdocs.yml), so
// a new command can't ship with an unreachable page.
func TestEveryCommandPageIsInDocsNav(t *testing.T) {
	nav, err := os.ReadFile("../../mkdocs.yml")
	if err != nil {
		t.Fatal(err)
	}
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			page := prefix + "_" + sub.Name()
			if !strings.Contains(string(nav), "reference/cli/"+page+".md") {
				t.Errorf("reference/cli/%s.md is not in mkdocs.yml's nav", page)
			}
			walk(sub, page)
		}
	}
	walk(newRootCmd(&rootFlags{}), "astrona")
}
