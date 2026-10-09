package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"astrona/internal/catalog"
	"astrona/internal/gitsource"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// catalogOrg is the GitHub organization whose trainings astrona lists by
// default (overridable with ASTRONA_CATALOG_ORG; empty disables it).
func catalogOrg() string {
	if v, ok := os.LookupEnv("ASTRONA_CATALOG_ORG"); ok {
		return v
	}
	return catalog.DefaultOrg
}

func catalogStore() (catalog.Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return catalog.Store{}, err
	}
	return catalog.Store{Dir: filepath.Join(home, ".astrona")}, nil
}

// loadCatalog returns the catalog: cached (up to an hour) unless refresh,
// else fetched — falling back to an older cache when fetching fails.
func loadCatalog(refresh bool) (catalog.Catalog, error) {
	store, err := catalogStore()
	if err != nil {
		return catalog.Catalog{}, err
	}
	if !refresh {
		if c, ok := store.Cached(time.Now()); ok {
			return c, nil
		}
	}
	sources, err := store.Sources()
	if err != nil {
		return catalog.Catalog{}, err
	}
	f := catalog.NewFetcher(func(gitURL string) ([]byte, error) {
		dir, err := gitsource.ResolveGitConfigSource(gitURL, "", false)
		if err != nil {
			return nil, err
		}
		return os.ReadFile(filepath.Join(dir, "astrona.yaml"))
	})
	c := f.Fetch(catalogOrg(), sources)
	if len(c.Trainings) == 0 && len(c.Errors) > 0 {
		if old, ok := store.Any(); ok {
			ui.Warnf("couldn't refresh the lab catalog (%s) — showing the one from %s", c.Errors[0], old.FetchedAt.Format(time.DateTime))
			return old, nil
		}
		return c, fmt.Errorf("couldn't load the lab catalog: %s", strings.Join(c.Errors, "; "))
	}
	if err := store.Save(c); err != nil {
		ui.Warnf("couldn't cache the lab catalog: %s", err)
	}
	return c, nil
}

// labArg applies a lab given as a command's argument (see useLabArg),
// resolving a catalog name (ATS014/section-010/module-01/lab-02) to its
// training repository and directory first. A path that exists on disk
// always wins over a catalog name.
func labArg(args []string, flags *rootFlags) error {
	if len(args) > 0 && args[0] != "" && !flags.gitExplicit {
		if _, err := os.Stat(args[0]); err != nil && catalog.LooksLikeLabID(args[0]) {
			c, err := loadCatalog(false)
			if err != nil {
				return err
			}
			t, l, ok := c.Find(args[0])
			if !ok {
				// The name may start with an owner (astrona-io/ATS014/…): hint at the training.
				training, rest, _ := strings.Cut(args[0], "/")
				if _, known := c.Training(training); !known {
					if second, _, ok := strings.Cut(rest, "/"); ok {
						if _, known := c.Training(second); known {
							training = second
						}
					}
				}
				return fmt.Errorf("no lab %s in the catalog — `astrona labs %s` lists that training's labs, `astrona labs --refresh` re-reads the catalog", args[0], training)
			}
			flags.labArg = args[0]
			flags.fromCurrent = false
			flags.catalogLab = l.ID
			flags.gitURL, flags.gitRef, flags.configPath = t.Repo, "", l.Path
			return nil
		}
	}
	useLabArg(args, flags)
	return nil
}

// catalogLabArg lets commands that take a running lab's name (destroy,
// status, shell, kubeconfig) also take its catalog name, the one students
// copy from astrona.io and use with run/submit: a catalog name (it has a "/",
// is not a path on disk and not a glob) points flags at that lab's config,
// as labArg does, and comes back as "" so the caller resolves the lab from
// its config. Any other name is returned unchanged.
func catalogLabArg(name string, flags *rootFlags) (string, error) {
	if name == "" || strings.ContainsAny(name, "*?[") || !catalog.LooksLikeLabID(name) {
		return name, nil
	}
	if _, err := os.Stat(name); err == nil {
		return name, nil
	}
	if err := labArg([]string{name}, flags); err != nil {
		return "", err
	}
	return "", nil
}

func newLabsCmd() *cobra.Command {
	var search, output string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "labs [training]",
		Short: "Browse the lab catalog: trainings and their labs, runnable by name",
		Long: "List the trainings astrona knows — the " + catalog.DefaultOrg + " repositories with the `" + catalog.Topic +
			"` topic, plus sources you add (`astrona labs add`) — or one training's labs. Every lab has a " +
			"catalog name you can run, use or test directly:\n\n" +
			"  astrona labs                       trainings\n" +
			"  astrona labs ATS014                its labs\n" +
			"  astrona labs --search routing      search lab titles\n" +
			"  astrona run ATS014/section-010/module-01/lab-02\n" +
			"  astrona run astrona-io/ATS014/section-010/module-01/lab-02   (the same, owner first)\n" +
			"  astrona run astrona-io/ATS014/section-010/module-01/playground\n\n" +
			"A lab from the catalog is a remote lab: it's fetched with git and asks for your trust " +
			"before it runs, like any --git lab. The catalog is cached for an hour (--refresh reads " +
			"it again).",
		Example: `  astrona labs
  astrona labs ATS014
  astrona labs --search "fault injection"
  astrona use ATS014/section-010/module-01/lab-02`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			store, err := catalogStore()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			c, _ := store.Any()
			var out []cobra.Completion
			for _, t := range c.Trainings {
				if strings.HasPrefix(strings.ToLower(t.ID), strings.ToLower(toComplete)) {
					out = append(out, cobra.CompletionWithDesc(t.ID, t.Title))
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			c, err := loadCatalog(refresh)
			if err != nil {
				return err
			}
			for _, e := range c.Errors {
				ui.Warnf("%s", e)
			}
			switch {
			case search != "":
				found := c.Search(search)
				if output == "json" {
					type hit struct {
						Training string      `json:"training"`
						Lab      catalog.Lab `json:"lab"`
					}
					out := []hit{}
					for _, h := range found {
						out = append(out, hit{h.Training.ID, h.Lab})
					}
					return printJSON(out)
				}
				if len(found) == 0 {
					fmt.Printf("No lab matches %q.\n", search)
					return nil
				}
				tw := tabwriter.NewWriter(os.Stdout, 0, 4, 3, ' ', 0)
				fmt.Fprintln(tw, "LAB\tTITLE")
				for _, h := range found {
					fmt.Fprintf(tw, "%s\t%s\n", h.Lab.ID, h.Lab.Title)
				}
				tw.Flush()
				fmt.Printf("\nRun one: astrona run <LAB>\n")
			case len(args) == 1:
				t, ok := c.Training(args[0])
				if !ok {
					return fmt.Errorf("no training %s in the catalog — `astrona labs` lists them", args[0])
				}
				if output == "json" {
					return printJSON(t)
				}
				fmt.Printf("%s — %s (%d labs)\n%s\n\n", t.ID, t.Title, len(t.Labs), ui.Paint(os.Stdout, t.Repo, ui.Dim))
				tw := tabwriter.NewWriter(os.Stdout, 0, 4, 3, ' ', 0)
				fmt.Fprintln(tw, "LAB\tTITLE")
				for _, l := range t.Labs {
					fmt.Fprintf(tw, "%s\t%s\n", l.ID, l.Title)
				}
				tw.Flush()
				fmt.Printf("\nRun one: astrona run <LAB>   (or pick it for every command: astrona use <LAB>)\n")
			default:
				if output == "json" {
					return printJSON(c)
				}
				if len(c.Trainings) == 0 {
					fmt.Println("No trainings found — `astrona labs add <git url>` adds one.")
					return nil
				}
				tw := tabwriter.NewWriter(os.Stdout, 0, 4, 3, ' ', 0)
				fmt.Fprintln(tw, "TRAINING\tLABS\tTITLE")
				for _, t := range c.Trainings {
					fmt.Fprintf(tw, "%s\t%d\t%s\n", t.ID, len(t.Labs), t.Title)
				}
				tw.Flush()
				fmt.Printf("\nIts labs: astrona labs <TRAINING>   ·   search: astrona labs --search <words>\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&search, "search", "", "Find labs whose title contains these words")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Read the catalog again instead of the cached copy (kept an hour)")
	addOutputFlag(cmd, &output)

	cmd.AddCommand(&cobra.Command{
		Use:   "add <git url>",
		Short: "Add a training repository (with an astrona.yaml) to your catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isGitURL(args[0]) {
				return fmt.Errorf("'%s' isn't a git URL (https://…, git@…, ssh://…)", args[0])
			}
			return editSources(func(src []string) ([]string, string) {
				for _, s := range src {
					if s == args[0] {
						return src, args[0] + " is already in your catalog"
					}
				}
				return append(src, args[0]), "Added " + args[0] + " — `astrona labs` lists it"
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <git url>",
		Short: "Remove a training repository you added",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return editSources(func(src []string) ([]string, string) {
				var out []string
				for _, s := range src {
					if s != args[0] {
						out = append(out, s)
					}
				}
				if len(out) == len(src) {
					return src, args[0] + " isn't one of your sources (`astrona labs sources`)"
				}
				return out, "Removed " + args[0]
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "sources",
		Short: "Show where the catalog comes from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := catalogStore()
			if err != nil {
				return err
			}
			src, err := store.Sources()
			if err != nil {
				return err
			}
			if org := catalogOrg(); org != "" {
				fmt.Printf("github.com/%s repositories with the %q topic\n", org, catalog.Topic)
			}
			for _, s := range src {
				fmt.Println(s)
			}
			return nil
		},
	})
	return cmd
}

func editSources(change func([]string) ([]string, string)) error {
	store, err := catalogStore()
	if err != nil {
		return err
	}
	src, err := store.Sources()
	if err != nil {
		return err
	}
	out, msg := change(src)
	if len(out) != len(src) {
		if err := store.SetSources(out); err != nil {
			return err
		}
	}
	fmt.Println(msg)
	return nil
}
