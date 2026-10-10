package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/lifecycle"
	"astrona/internal/resources"

	"github.com/spf13/cobra"
)

// resourceDirEnv is set for a running resource: the folder holding the
// lab's copy of its resources, so one resource can find another.
const resourceDirEnv = "ASTRONA_RESOURCE_DIR"

func newResourceCmd(flags *rootFlags) *cobra.Command {
	var labFlag string
	cmd := &cobra.Command{
		Use:     "resource",
		Aliases: []string{"res", "resources"},
		Short:   "Show, copy or run the files a lab ships for you (its resources)",
		Long: "A lab can ship files you need — a setup script, a YAML file to fix, a small program — " +
			"in its resources/ folder instead of asking you to paste them from the docs. `astrona run` " +
			"copies them to ~/.astrona/resources/<lab> and lists them; nothing runs until you ask.\n\n" +
			"  astrona res                       list the lab's resources\n" +
			"  astrona res show <name>           print one (a folder: its files)\n" +
			"  astrona res copy <name> [dest]    copy it here (or to dest) to edit\n" +
			"  astrona res path <name>           print its path, for $(…) in your own commands\n" +
			"  astrona res run <name> [args…]    run it\n\n" +
			"Which lab: --lab, else the lab of the `astrona shell` you're in ($ASTRONA_LAB), else the " +
			"lab of -c/--git, else the one picked with `astrona use`, else the only lab with resources. " +
			"With several labs open in several terminals, run `astrona shell <lab>` in each and every " +
			"`astrona res` there works on that lab.\n\n" +
			"How `run` runs one: its run command from the lab (in its folder), .sh with bash, .yaml " +
			"with kubectl apply -f, an executable with #! directly — in a kind lab always with " +
			"KUBECONFIG set to the lab's own kubeconfig, so nothing reaches your other clusters " +
			"($ASTRONA_LAB and $" + resourceDirEnv + " are set). In a qemu lab it's copied into the VM " +
			"(~/" + vmResourceDir + "/<name>) and run there as the account `astrona ssh` uses; --vm picks " +
			"the VM of a multi-VM lab. A plain file can only be shown or copied.",
		Example: `  astrona res
  astrona res run setup-db
  astrona res copy broken-deployment
  kubectl diff -f $(astrona res path broken-deployment)
  astrona res run load-test --lab ats-014-lab-010-01 -- --rps 50`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, list, dir, err := resourceLab(cmd, flags, labFlag)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Printf("Lab %s has no resources.\n", lab)
				return nil
			}
			printResources(os.Stdout, dir, list)
			fmt.Printf("\nUse: astrona res show|copy|path|run <name>\n")
			return nil
		},
	}
	cmd.PersistentFlags().StringVar(&labFlag, "lab", "", "The lab whose resources to use (default: $ASTRONA_LAB, -c/--git, `astrona use`, else the only lab with resources)")

	withResource := func(use, short string, args cobra.PositionalArgs, fn func(cmd *cobra.Command, lab, dir string, r resources.Resource, rest []string) error) *cobra.Command {
		return &cobra.Command{
			Use:          use,
			Short:        short,
			Args:         args,
			SilenceUsage: true,
			// The resource's name completes from the lab's copy; what
			// follows (copy's dest, run's args) completes as files.
			ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
				if len(args) > 0 {
					return nil, cobra.ShellCompDirectiveDefault
				}
				_, list, _, err := resourceLab(cmd, flags, labFlag)
				if err != nil {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				return resourceCompletions(list), cobra.ShellCompDirectiveNoFileComp
			},
			RunE: func(cmd *cobra.Command, args []string) error {
				lab, list, dir, err := resourceLab(cmd, flags, labFlag)
				if err != nil {
					return err
				}
				r, ok := resources.Find(list, args[0])
				if !ok {
					return fmt.Errorf("lab %s has no resource %q — %s", lab, args[0], resourceNames(list))
				}
				return fn(cmd, lab, dir, r, args[1:])
			},
		}
	}

	cmd.AddCommand(&cobra.Command{
		Use:          "list",
		Short:        "List the lab's resources",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         cmd.RunE,
	})
	cmd.AddCommand(withResource("show <name>", "Print a resource (a folder: the files in it)", cobra.ExactArgs(1),
		func(_ *cobra.Command, _, dir string, r resources.Resource, _ []string) error {
			p, err := r.Path(dir)
			if err != nil {
				return err
			}
			if r.Dir {
				return printResourceTree(p)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fmt.Print(sanitizeTerminalText(string(b)))
			if len(b) > 0 && b[len(b)-1] != '\n' {
				fmt.Println()
			}
			return nil
		}))
	var force bool
	copyCmd := withResource("copy <name> [dest]", "Copy a resource into the current folder (or dest) to work on it", cobra.RangeArgs(1, 2),
		func(_ *cobra.Command, _, dir string, r resources.Resource, rest []string) error {
			dest := "."
			if len(rest) > 0 {
				dest = rest[0]
			}
			to, err := r.CopyTo(dir, dest, force)
			if err != nil {
				return err
			}
			fmt.Printf("Copied %s to %s\n", r.Name, to)
			return nil
		})
	copyCmd.Flags().BoolVar(&force, "force", false, "Replace the file or folder if it already exists")
	cmd.AddCommand(copyCmd)
	cmd.AddCommand(withResource("path <name>", "Print a resource's path (for $(…) in your own commands)", cobra.ExactArgs(1),
		func(_ *cobra.Command, _, dir string, r resources.Resource, _ []string) error {
			p, err := r.Path(dir)
			if err != nil {
				return err
			}
			fmt.Println(p)
			return nil
		}))
	var vmFlag string
	runCmd := withResource("run <name> [args...]", "Run a resource against the lab (in a qemu lab: inside its VM)", cobra.MinimumNArgs(1),
		func(_ *cobra.Command, lab, dir string, r resources.Resource, rest []string) error {
			return runResource(lab, dir, r, rest, vmFlag)
		})
	runCmd.Flags().StringVar(&vmFlag, "vm", "", "qemu labs: the VM to run it in (default: the resource's vm:, else the lab's only VM)")
	cmd.AddCommand(runCmd)
	return cmd
}

// resourceLab finds the lab `astrona resource` works on (see the command's
// Long text for the order) and loads its copy of its resources.
func resourceLab(cmd *cobra.Command, flags *rootFlags, labFlag string) (string, []resources.Resource, string, error) {
	lab, err := pickResourceLab(cmd, flags, labFlag)
	if err != nil {
		return "", nil, "", err
	}
	list, dir, err := resources.Load(lab)
	if err != nil {
		return "", nil, "", err
	}
	return lab, list, dir, nil
}

func pickResourceLab(cmd *cobra.Command, flags *rootFlags, labFlag string) (string, error) {
	if labFlag != "" {
		name, err := catalogLabArg(labFlag, flags) // a catalog name resolves to its config
		if err != nil {
			return "", err
		}
		if name != "" {
			return config.NormalizeClusterName(name), nil
		}
		return labNameFromConfig(flags)
	}
	if env := os.Getenv(labShellEnvVar); env != "" {
		return config.NormalizeClusterName(env), nil
	}
	if cmd.Flags().Changed("config") || cmd.Flags().Changed("git") || flags.fromCurrent {
		return labNameFromConfig(flags)
	}
	labs, err := resources.Labs()
	if err != nil {
		return "", err
	}
	switch len(labs) {
	case 1:
		return labs[0], nil
	case 0:
		return "", fmt.Errorf("no lab has resources — they're copied when a lab that has some starts (`astrona run`)")
	}
	return "", fmt.Errorf("several labs have resources (%s) — pick one: --lab <lab>, or run `astrona shell <lab>` and use `astrona res` in there", strings.Join(trimAstro(labs), ", "))
}

// labNameFromConfig is the lab of -c/--git/`astrona use`, by its config.
func labNameFromConfig(flags *rootFlags) (string, error) {
	cfg, _, cleanup, err := LoadLabForCommand(flags)
	if err != nil {
		return "", err
	}
	cleanup()
	return config.NormalizeClusterName(cfg.Metadata.Name), nil
}

func trimAstro(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.TrimPrefix(n, "astro-")
	}
	return out
}

func resourceNames(list []resources.Resource) string {
	if len(list) == 0 {
		return "it has none"
	}
	names := make([]string, len(list))
	for i, r := range list {
		names[i] = r.Name
	}
	return "it has: " + strings.Join(names, ", ")
}

// printResourceTree lists the files in a folder resource.
func printResourceTree(root string) error {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		fmt.Println(f)
	}
	return nil
}

// resourceCommand is the command that runs r (from its lab's copy, dir)
// with args, and the folder it runs in. The lab's own run command is split
// on spaces and started without a shell — pipes and redirects belong in a
// script.
func resourceCommand(dir string, r resources.Resource, args []string) (name string, argv []string, workDir string, err error) {
	p, err := r.Path(dir)
	if err != nil {
		return "", nil, "", err
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", nil, "", err
	}
	switch r.How {
	case resources.HowBash:
		return "bash", append([]string{p}, args...), wd, nil
	case resources.HowApply:
		return "kubectl", append([]string{"apply", "-f", p}, args...), wd, nil
	case resources.HowExec:
		return p, args, wd, nil
	case resources.HowCommand:
		fields := strings.Fields(r.Run)
		if len(fields) == 0 {
			return "", nil, "", fmt.Errorf("resource %s has an empty run command", r.Name)
		}
		in := p
		if !r.Dir {
			in = filepath.Dir(p)
		}
		return fields[0], append(fields[1:], args...), in, nil
	}
	return "", nil, "", fmt.Errorf("%s is a file, not something to run — `astrona res show %s` prints it, `astrona res copy %s` copies it here", r.Name, r.Name, r.Name)
}

// runResource runs r against lab: for a kind lab with KUBECONFIG pointing at
// the lab's own kubeconfig (and its linked clusters'), like `astrona shell`.
func runResource(lab, dir string, r resources.Resource, args []string, vmFlag string) error {
	if !kindClusterExists(lab) {
		return runResourceInVM(lab, dir, r, args, vmFlag) // qemu, or not running: it says so
	}
	if vmFlag != "" {
		return fmt.Errorf("--vm is for qemu labs; %s is a kind lab", strings.TrimPrefix(lab, "astro-"))
	}
	name, argv, workDir, err := resourceCommand(dir, r, args)
	if err != nil {
		return err
	}
	kubeconfig, err := labKubeconfig(lab)
	if err != nil {
		return err
	}
	links, linkEnv := lifecycle.Links(lab)
	env := append(os.Environ(),
		"KUBECONFIG="+strings.Join(shellKubeconfigs(kubeconfig, lab, links, cluster.ExistingKubeconfig), string(os.PathListSeparator)),
		labShellEnvVar+"="+lab,
		resourceDirEnv+"="+dir,
	)
	env = append(env, linkEnv...)
	env = append(env, lifecycle.CAEnv(lab)...)

	c := exec.Command(name, argv...)
	c.Dir = workDir
	c.Env = env
	fmt.Fprintf(os.Stderr, "→ %s  (lab %s)\n", strings.Join(append([]string{r.Command()}, args...), " "), strings.TrimPrefix(lab, "astro-"))
	return runAttached(c)
}

// resourceCompletions are a lab's resources as completions: name, and what
// it is (its description, else how it runs).
func resourceCompletions(list []resources.Resource) []cobra.Completion {
	out := make([]cobra.Completion, len(list))
	for i, r := range list {
		desc := r.Description
		if desc == "" {
			desc = r.Command()
		}
		if desc == "" {
			desc = "file"
		}
		out[i] = cobra.CompletionWithDesc(r.Name, desc)
	}
	return out
}
