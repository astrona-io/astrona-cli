package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// currentLab is the lab `astrona use` remembers: the same inputs -c/-f/
// --git/--git-ref take. Saved in ~/.astrona/current.json.
type currentLab struct {
	Config string `json:"config"`
	File   string `json:"file,omitempty"`
	Git    string `json:"git,omitempty"`
	GitRef string `json:"gitRef,omitempty"`
	Name   string `json:"name,omitempty"` // metadata.name, for display
}

func currentLabPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".astrona", "current.json"), nil
}

func loadCurrentLab() (*currentLab, error) {
	path, err := currentLabPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c currentLab
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

func saveCurrentLab(c *currentLab) error {
	path, err := currentLabPath()
	if err != nil {
		return err
	}
	if c == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// applyCurrentLab points flags at the remembered lab when the command
// didn't name one: no -c/--git given and no lab config in the current
// directory (working inside a lab directory always wins).
func applyCurrentLab(cmd *cobra.Command, flags *rootFlags) {
	if cmd.Flags().Changed("config") || cmd.Flags().Changed("git") {
		return
	}
	if _, err := os.Stat(filepath.Join(".", flags.fileName)); err == nil {
		return
	}
	c, err := loadCurrentLab()
	if err != nil || c == nil {
		return
	}
	flags.configPath, flags.gitURL, flags.gitRef = c.Config, c.Git, c.GitRef
	flags.fromCurrent = true
	if c.File != "" && !cmd.Flags().Changed("file") {
		flags.fileName = c.File
	}
}

// useLabArg applies a lab given as a command's argument — a directory or
// config file path, a config URL (…/config.yaml), or a git repository URL
// — overriding the remembered lab.
func useLabArg(args []string, flags *rootFlags) {
	if len(args) == 0 || args[0] == "" {
		return
	}
	arg := args[0]
	flags.labArg = arg
	flags.fromCurrent = false
	if flags.gitExplicit && !isGitURL(arg) {
		flags.configPath = arg // a subdirectory of the --git repo
		return
	}
	switch {
	case isGitURL(arg):
		flags.gitURL, flags.configPath = arg, "."
	case strings.HasSuffix(arg, ".yaml") || strings.HasSuffix(arg, ".yml"):
		if strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "http://") {
			flags.gitURL, flags.configPath = "", arg
			return
		}
		flags.gitURL, flags.configPath, flags.fileName = "", filepath.Dir(arg), filepath.Base(arg)
	default:
		flags.gitURL, flags.configPath = "", arg
	}
}

// isGitURL: git@host:…, ssh://…, …​.git, or an https URL that isn't a
// YAML file (e.g. https://github.com/org/labs).
func isGitURL(s string) bool {
	switch {
	case strings.HasPrefix(s, "git@"), strings.HasPrefix(s, "ssh://"), strings.HasSuffix(s, ".git"):
		return true
	case strings.HasPrefix(s, "https://"):
		return !strings.HasSuffix(s, ".yaml") && !strings.HasSuffix(s, ".yml")
	}
	return false
}

func newUseCmd(flags *rootFlags) *cobra.Command {
	var forget bool
	var output string
	cmd := &cobra.Command{
		Use:   "use [lab]",
		Short: "Pick the lab every other command works on (like kubectl's current context)",
		Long: "Remember a lab so you don't have to pass -c/--git to every command: a lab directory, " +
			"a config file, a config URL (…/config.yaml) or a git repository (with -c for a " +
			"subdirectory and --git-ref for a branch or tag).\n\n" +
			"Commands use it when you don't name a lab and the current directory has no lab " +
			"config — working inside a lab directory always wins. `run`, `submit`, `reset`, `test` " +
			"and `validate` also take the lab as their argument.\n\n" +
			"Without an argument, shows the current lab. --clear forgets it.",
		Example: `  astrona use ./labs/k8s-web-01
  astrona use https://github.com/org/labs -c labs/net-01 --git-ref v2
  astrona use            # which lab?
  astrona use --clear`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if forget {
				if err := saveCurrentLab(nil); err != nil {
					return err
				}
				fmt.Println("No current lab — commands use -c/--git or the lab in the current directory.")
				return nil
			}
			if len(args) == 0 && !cmd.Flags().Changed("git") && !cmd.Flags().Changed("config") {
				c, err := loadCurrentLab()
				if err != nil {
					return err
				}
				if err := checkOutput(output); err != nil {
					return err
				}
				if output == "json" {
					return printJSON(c) // null when no lab is picked
				}
				if c == nil {
					fmt.Println("No current lab. Pick one: astrona use <lab-dir | config URL | git URL>")
					return nil
				}
				fmt.Printf("Current lab: %s (%s)\n", c.Name, describeCurrentLab(c))
				return nil
			}

			// The flags as given (not the remembered lab), plus the argument.
			if !cmd.Flags().Changed("config") {
				flags.configPath = "."
			}
			if !cmd.Flags().Changed("git") {
				flags.gitURL = ""
			}
			useLabArg(args, flags)
			cfg, _, cleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			cleanup()

			c := &currentLab{Config: flags.configPath, File: flags.fileName, Git: flags.gitURL, GitRef: flags.gitRef, Name: cfg.Metadata.Name}
			if c.Git == "" && !strings.HasPrefix(c.Config, "http") {
				abs, err := filepath.Abs(c.Config)
				if err != nil {
					return err
				}
				c.Config = abs
			}
			if c.File == "config.yaml" {
				c.File = ""
			}
			if err := saveCurrentLab(c); err != nil {
				return err
			}
			fmt.Printf("Now using lab %s (%s).\nNext: astrona run\n", c.Name, describeCurrentLab(c))
			return nil
		},
	}
	cmd.Flags().BoolVar(&forget, "clear", false, "Forget the current lab")
	addOutputFlag(cmd, &output)
	return cmd
}

func describeCurrentLab(c *currentLab) string {
	where := c.Config
	if c.Git != "" {
		where = c.Git
		if c.Config != "" && c.Config != "." {
			where += " → " + c.Config
		}
		if c.GitRef != "" {
			where += " @ " + c.GitRef
		}
	}
	if c.File != "" {
		where += " (" + c.File + ")"
	}
	return where
}
