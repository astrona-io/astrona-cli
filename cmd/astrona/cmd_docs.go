package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"astrona/internal/catalog"
	"astrona/internal/config"

	"github.com/spf13/cobra"
)

// maxDocBytes bounds a lab doc fetched from a URL.
const maxDocBytes = 2 * 1024 * 1024

// labDoc is one entry of metadata.docs as `astrona docs` exposes it.
type labDoc struct {
	key, title, path string
	spoiler          bool // shown with a warning: it gives the solution away
}

func labDocs(cfg *config.LabConfig) []labDoc {
	d := cfg.Metadata.Docs
	all := []labDoc{
		{"question", "The task", d.Question, false},
		{"case-study", "Case study — the same task, with hints", d.CaseStudy, false},
		{"prerequisites", "Prerequisites — what to know first", d.Prerequisites, false},
		{"solution", "Solution — step by step", d.Solution, true},
	}
	var out []labDoc
	for _, doc := range all {
		if doc.path != "" {
			out = append(out, doc)
		}
	}
	return out
}

// readLabDoc reads a doc relative to the lab's config: within baseDir for
// a local/git config (JoinStrictlyWithinBaseDir — a doc path must be
// relative and can't escape it), or
// resolved against the config's URL (https only, size-capped) for a remote
// config.
func readLabDoc(configPath, docPath string) (string, error) {
	if strings.HasPrefix(configPath, "http://") || strings.HasPrefix(configPath, "https://") {
		base, err := url.Parse(configPath)
		if err != nil {
			return "", fmt.Errorf("parse config URL: %w", err)
		}
		if strings.Contains(docPath, "://") || strings.HasPrefix(docPath, "/") {
			return "", fmt.Errorf("doc path '%s' must be relative to the config", docPath)
		}
		ref := *base
		ref.Path = path.Join(path.Dir(base.Path), docPath)
		if !strings.HasPrefix(ref.Path, path.Dir(base.Path)+"/") {
			return "", fmt.Errorf("doc path '%s' escapes the config's location", docPath)
		}
		tmp, cleanup, err := config.DownloadToTemp(ref.String(), "astrona-doc-*.md", maxDocBytes)
		if err != nil {
			return "", fmt.Errorf("fetch %s: %w", ref.String(), err)
		}
		defer cleanup()
		data, err := os.ReadFile(tmp)
		return string(data), err
	}

	full, err := config.JoinStrictlyWithinBaseDir(filepath.Dir(configPath), docPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", docPath, err)
	}
	return string(data), nil
}

// docKeys are the docs `astrona docs` can show; "guide" is the older name
// of "solution" and still works.
var docKeys = []string{"question", "case-study", "prerequisites", "solution", "guide"}

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
			return "", "", fmt.Errorf("unexpected argument %q — expected one of %s and/or a catalog lab (astrona.io/ATS016/section-020/module-01/lab-01)", a, strings.Join(docKeys[:4], ", "))
		}
	}
	return key, lab, nil
}

func newDocsCmd(flags *rootFlags) *cobra.Command {
	var noPager bool

	cmd := &cobra.Command{
		Use:   "docs [question|case-study|prerequisites|solution] [catalog-lab]",
		Short: "Read the lab's docs (task, hints, prerequisites, solution) in the terminal",
		Long: "Show a lab's own documentation, rendered for the terminal and paged ($PAGER, else " +
			"less; --no-pager to print). With no argument, lists what the lab provides.\n\n" +
			"  question       the task to solve (question.md, or metadata.docs.question)\n" +
			"  case-study     the same task with more guidance (case-study.md, or metadata.docs.caseStudy)\n" +
			"  prerequisites  what to know before starting (prerequisites.md, or metadata.docs.prerequisites)\n" +
			"  solution       the full step-by-step solution (solution.md, or metadata.docs.solution) — " +
			"spoilers; `guide` works too\n\n" +
			"A doc metadata.docs doesn't list is found by that file name next to config.yaml.\n\n" +
			"Uses the lab config from -c/--file/--git (local, git or URL), or a catalog lab named as an " +
			"argument (`astrona docs question astrona.io/ATS016/section-020/module-01/lab-01`).",
		Example: `  astrona docs -c ./labs/my-lab
  astrona docs question -c ./labs/my-lab`,
		ValidArgs: docKeys,
		Args:      cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, lab, err := splitDocsArgs(args)
			if err != nil {
				return err
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

			docs := labDocs(cfg)
			if len(args) == 0 {
				printDocList(os.Stdout, cfg.Metadata.Name, docs)
				return nil
			}

			var doc *labDoc
			for i := range docs {
				if docs[i].key == args[0] {
					doc = &docs[i]
				}
			}
			if doc == nil {
				return fmt.Errorf("lab '%s' has no %s doc (no %s next to its config.yaml, none in metadata.docs) — `astrona docs` lists what it has", cfg.Metadata.Name, args[0], docFileFor(args[0]))
			}

			text, err := readLabDoc(finalPath, doc.path)
			if err != nil {
				return err
			}

			w, paged, closePager, err := openPager(noPager)
			if err != nil {
				return err
			}
			defer closePager()
			if doc.spoiler {
				fmt.Fprintln(w, colorize(ansiYellow, "⚠  This is the full solution."))
				fmt.Fprintln(w)
			}
			color := os.Getenv("NO_COLOR") == "" && (paged || colorsEnabled())
			fmt.Fprint(w, renderMarkdown(sanitizeTerminalText(text), color))
			return nil
		},
	}

	cmd.Flags().BoolVar(&noPager, "no-pager", false, "Print directly instead of using a pager")
	return cmd
}

func printDocList(w io.Writer, lab string, docs []labDoc) {
	if len(docs) == 0 {
		fmt.Fprintf(w, "Lab '%s' has no docs (metadata.docs is empty).\n", lab)
		return
	}
	fmt.Fprintf(w, "Docs for %s:\n\n", lab)
	for _, d := range docs {
		fmt.Fprintf(w, "  astrona docs %-14s %s\n", d.key, d.title)
	}
}

// docFileFor is the usual file name of a doc key ("case-study" →
// case-study.md).
func docFileFor(key string) string {
	if key == "case-study" {
		return config.DocFileNames["caseStudy"]
	}
	return config.DocFileNames[key]
}
