package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"astrona/internal/catalog"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/resources"

	"github.com/spf13/cobra"
)

// docKeys are the docs `astrona docs` can show; "guide" is the older name
// of "solution" and still works. "solutions" lists the lab's solutions.
var docKeys = []string{"question", "case-study", "prerequisites", "solution", "solutions", "guide"}

// splitDocsArgs sorts `astrona docs` arguments into a doc key and a catalog
// lab, in either order: `docs question ATS016/…`, `docs ATS016/…`.
func splitDocsArgs(args []string) (key, lab string, err error) {
	for _, a := range args {
		switch {
		case slices.Contains(docKeys, a) && key == "":
			key = a
			if key == "guide" {
				key = "solution"
			}
		case catalog.LooksLikeLabID(a) && lab == "":
			lab = a
		default:
			return "", "", fmt.Errorf("unexpected argument %q — expected one of %s and/or a catalog lab (astrona.io/ATS016/section-020/module-01/lab-01)", a, strings.Join(docKeys[:5], ", "))
		}
	}
	return key, lab, nil
}

func newDocsCmd(flags *rootFlags) *cobra.Command {
	var noPager, jsonOut bool
	var solutionName string

	cmd := &cobra.Command{
		Use:   "docs [question|case-study|prerequisites|solution|solutions] [catalog-lab]",
		Short: "Read the lab's docs (task, hints, prerequisites, solution) in the terminal",
		Long: "Show a lab's own documentation, rendered for the terminal and paged ($PAGER, else " +
			"less; --no-pager to print). With no argument, lists what the lab provides.\n\n" +
			"  question       the task to solve (question.md, or metadata.docs.question)\n" +
			"  case-study     the same task with more guidance (case-study.md, or metadata.docs.caseStudy)\n" +
			"  prerequisites  what to know before starting (prerequisites.md, or metadata.docs.prerequisites)\n" +
			"  solution       the full step-by-step solution (solution.md, or metadata.docs.solution) — " +
			"spoilers; `guide` works too. --name <name> shows another solution (solution.<name>.md)\n" +
			"  solutions      lists the lab's solutions in order (--json for a machine-readable list); " +
			"none for an exam or a playground\n\n" +
			"A doc metadata.docs doesn't list is found by that file name next to config.yaml. A lab may " +
			"have several solutions: the default (solution.md) and any solution.<name>.md next to config.yaml, " +
			"with optional front matter title, order and summary. A lab loaded from a URL has only the default.\n\n" +
			"Uses the lab config from -c/--file/--git (local, git or URL), or a catalog lab named as an " +
			"argument (`astrona docs question astrona.io/ATS016/section-020/module-01/lab-01`).",
		Example: `  astrona docs -c ./labs/my-lab
  astrona docs question -c ./labs/my-lab
  astrona docs solutions --json -c ./labs/my-lab
  astrona docs solution --name web-ui -c ./labs/my-lab`,
		ValidArgs: docKeys,
		Args:      cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, lab, err := splitDocsArgs(args)
			if err != nil {
				return err
			}
			if solutionName != "" && key != "solution" {
				return fmt.Errorf("--name picks one of the lab's solutions: use it with `astrona docs solution`")
			}
			if jsonOut && key != "solutions" {
				return fmt.Errorf("--json lists the lab's solutions: use it with `astrona docs solutions`")
			}
			if lab != "" {
				if err := labArg([]string{lab}, flags); err != nil {
					return err
				}
			}
			args = nil
			if key != "" {
				args = []string{key}
			}
			finalPath, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, flags.verbose)
			if err != nil {
				return withNoLabHint(err, flags)
			}
			cfg, cleanup, err := config.LoadLabConfig(finalPath)
			if err != nil {
				return err
			}
			defer cleanup()

			readDoc := func(file string) (string, error) { return labops.ReadLabDoc(finalPath, file) }
			docs := labops.LabDocs(cfg)
			if len(args) == 0 {
				printDocList(os.Stdout, cfg.Metadata.Name, docs, extraSolutions(cfg, readDoc))
				printDocResources(os.Stdout, cfg, finalPath)
				return nil
			}
			if args[0] == "solutions" {
				var list []labops.Solution
				if !docsHideSolutions(cfg, finalPath, flags) {
					list = labops.LabSolutions(cfg, readDoc)
				}
				return printSolutionList(os.Stdout, cfg.Metadata.Name, list, jsonOut)
			}

			var doc *labops.LabDoc
			for i := range docs {
				if docs[i].Key == args[0] {
					doc = &docs[i]
				}
			}
			if args[0] == "solution" && solutionName != "" && solutionName != config.DefaultSolutionName {
				file, err := namedSolutionFile(cfg, finalPath, flags, solutionName)
				if err != nil {
					return err
				}
				doc = &labops.LabDoc{Key: "solution", Path: file, Spoiler: true}
			}
			if doc == nil {
				return fmt.Errorf("lab '%s' has no %s doc (no %s next to its config.yaml, none in metadata.docs) — `astrona docs` lists what it has", cfg.Metadata.Name, args[0], labops.DocFileFor(args[0]))
			}

			text, err := labops.ReadLabDoc(finalPath, doc.Path)
			if err != nil {
				return err
			}

			w, paged, closePager, err := openPager(noPager)
			if err != nil {
				return err
			}
			defer closePager()
			if doc.Spoiler {
				fmt.Fprintln(w, colorize(ansiYellow, "⚠  This is the full solution."))
				fmt.Fprintln(w)
			}
			color := os.Getenv("NO_COLOR") == "" && (paged || colorsEnabled())
			title, body := stripFrontMatter(labops.SanitizeTerminalText(text))
			if title != "" {
				body = "# " + title + "\n\n" + body
			}
			fmt.Fprint(w, renderMarkdown(body, color))
			return nil
		},
	}

	cmd.Flags().BoolVar(&noPager, "no-pager", false, "Print directly instead of using a pager")
	cmd.Flags().StringVar(&solutionName, "name", "", "With solution: the solution to show (default, or the `name` in solution.<name>.md; astrona docs solutions lists them)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "With solutions: print the list as JSON")
	return cmd
}

// docsHideSolutions: `astrona docs solutions` lists none for an exam or a
// playground, which never show a solution.
func docsHideSolutions(cfg *config.LabConfig, configPath string, flags *rootFlags) bool {
	if cfg.Exam.Enabled() || labops.IsPlaygroundName(flags.catalogLab) {
		return true
	}
	if strings.HasPrefix(configPath, "https://") || strings.HasPrefix(configPath, "http://") {
		return false
	}
	return filepath.Base(filepath.Dir(configPath)) == "playground"
}

// namedSolutionFile is the file of the lab's solution called name, among
// those `astrona docs solutions` lists.
func namedSolutionFile(cfg *config.LabConfig, configPath string, flags *rootFlags, name string) (string, error) {
	if !config.ValidSolutionName(name) {
		return "", fmt.Errorf("%q isn't a solution name — names are lowercase words joined by hyphens (web-ui); `astrona docs solutions` lists them", name)
	}
	if !docsHideSolutions(cfg, configPath, flags) {
		for _, s := range cfg.Metadata.Docs.Solutions {
			if s.Name == name {
				return s.File, nil
			}
		}
	}
	if strings.HasPrefix(configPath, "https://") || strings.HasPrefix(configPath, "http://") {
		return "", fmt.Errorf("lab '%s' has no solution %q: a lab loaded from a URL only has its default solution", cfg.Metadata.Name, name)
	}
	return "", fmt.Errorf("lab '%s' has no solution %q (no %s next to its config.yaml) — `astrona docs solutions` lists them", cfg.Metadata.Name, name, config.SolutionFileName(name))
}

// extraSolutions are the lab's solutions other than the default, for the
// `astrona docs` list.
func extraSolutions(cfg *config.LabConfig, read func(string) (string, error)) []labops.Solution {
	if len(cfg.Metadata.Docs.Solutions) == 0 {
		return nil
	}
	var out []labops.Solution
	for _, s := range labops.LabSolutions(cfg, read) {
		if s.Name != config.DefaultSolutionName {
			out = append(out, s)
		}
	}
	return out
}

// solutionListJSON is `astrona docs solutions --json`.
type solutionListJSON struct {
	Solutions []labops.Solution `json:"solutions"`
}

// printSolutionList prints the lab's solutions in order, as text or JSON.
// A solution that cannot be read fails the list: it would fail
// `astrona docs solution --name` too.
func printSolutionList(w io.Writer, lab string, list []labops.Solution, asJSON bool) error {
	for _, s := range list {
		if s.Err != nil {
			return fmt.Errorf("solution %q: %w", s.Name, s.Err)
		}
	}
	if asJSON {
		if list == nil {
			list = []labops.Solution{}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(solutionListJSON{Solutions: list})
	}
	if len(list) == 0 {
		fmt.Fprintf(w, "Lab '%s' has no solutions to show.\n", lab)
		return nil
	}
	fmt.Fprintf(w, "Solutions for %s:\n\n", lab)
	for _, s := range list {
		command := "astrona docs solution"
		if s.Name != config.DefaultSolutionName {
			command += " --name " + s.Name
		}
		line := s.Title
		if s.Summary != "" {
			line += " — " + s.Summary
		}
		fmt.Fprintf(w, "  %-40s %s\n", command, labops.SanitizeLine(line))
	}
	return nil
}

func printDocList(w io.Writer, lab string, docs []labops.LabDoc, extra []labops.Solution) {
	if len(docs) == 0 {
		fmt.Fprintf(w, "Lab '%s' has no docs (metadata.docs is empty).\n", lab)
		return
	}
	fmt.Fprintf(w, "Docs for %s:\n\n", lab)
	for _, d := range docs {
		fmt.Fprintf(w, "  astrona docs %-14s %s\n", d.Key, d.Title)
	}
	for _, s := range extra {
		fmt.Fprintf(w, "  astrona docs solution --name %s  %s\n", s.Name, labops.SanitizeLine("Solution — "+s.Title))
	}
}

// printDocResources adds the lab's resources to `astrona docs`'s list, so a
// student sees them next to the task that uses them.
func printDocResources(w io.Writer, cfg *config.LabConfig, configPath string) {
	if strings.HasPrefix(configPath, "https://") || strings.HasPrefix(configPath, "http://") {
		return
	}
	list, err := resources.Collect(cfg, filepath.Dir(configPath))
	if err != nil || len(list) == 0 {
		return
	}
	fmt.Fprintf(w, "\nResources (astrona res show|copy|run <name>):\n\n")
	for _, r := range list {
		desc := r.Description
		if desc == "" {
			desc = r.File
		}
		fmt.Fprintf(w, "  %-28s %s\n", labops.SanitizeLine(r.Name), labops.SanitizeLine(desc))
	}
}
