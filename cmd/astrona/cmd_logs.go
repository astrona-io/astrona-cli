package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"astrona/internal/logs"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// newLogsCmd builds `astrona logs` (alias `log`), the read side of the
// per-run log files internal/ui writes under ~/.astrona/logs. Every
// lifecycle command (run, test, submit, destroy) tees its full output to
// one such file; nothing until now read them back.
//
// A run is referenced by, in order of convenience: nothing / "latest" (the
// newest), its index from `astrona logs list` (1 = newest), the UTC stamp
// in its filename, or an absolute path to the file.
func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "logs",
		Aliases: []string{"log"},
		Short:   "Inspect the per-run logs astrona writes under ~/.astrona/logs",
		Long: "Inspect the per-run logs astrona writes under ~/.astrona/logs.\n\n" +
			"Every `run`, `test`, `submit`, and `destroy` tees its full output to a " +
			"timestamped log file. `astrona logs` lists those runs, shows one through " +
			"your pager, prints a log's path for scripting, exports logs out of " +
			"~/.astrona, and prunes old ones.",
	}
	cmd.AddCommand(
		newLogsListCmd(),
		newLogsViewCmd(),
		newLogsPathCmd(),
		newLogsExportCmd(),
		newLogsCleanCmd(),
	)
	return cmd
}

// logFlags carries the run-selection flags shared across the subcommands.
// bindSelect wires the two every subcommand wants; bindListFilters adds the
// window/count narrowing that only `list` exposes.
type logFlags struct {
	lab     string
	command string
	since   time.Duration
	last    int
}

func (l logFlags) filter() logs.Filter {
	return logs.Filter{Lab: l.lab, Command: l.command, Since: l.since, Last: l.last}
}

func bindSelect(c *cobra.Command, l *logFlags) {
	c.Flags().StringVar(&l.lab, "lab", "", "Only logs for this lab")
	c.Flags().StringVar(&l.command, "command", "", "Only logs for this command (run, test, submit, destroy)")
}

func bindListFilters(c *cobra.Command, l *logFlags) {
	bindSelect(c, l)
	c.Flags().DurationVar(&l.since, "since", 0, "Only logs started within this window (e.g. 24h, 90m)")
	c.Flags().IntVar(&l.last, "last", 0, "Keep only the N newest logs after the other filters")
}

// --- logs list -------------------------------------------------------------

func newLogsListCmd() *cobra.Command {
	var lf logFlags
	var format string

	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List recorded runs, newest first",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := logs.List(lf.filter())
			if err != nil {
				return err
			}
			return renderEntries(os.Stdout, entries, format)
		},
	}

	bindListFilters(cmd, &lf)
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json, yaml")
	return cmd
}

func renderEntries(w io.Writer, entries []logs.Entry, format string) error {
	switch format {
	case "", "table":
		printLogTable(w, entries)
		return nil
	case "json":
		return writeJSON(w, entries)
	case "yaml":
		return writeYAML(w, entries)
	default:
		return fmt.Errorf("unknown --format %q (want table, json, or yaml)", format)
	}
}

// printLogTable renders entries in the same kubectl-get-style aligned table
// `astrona list` / `astrona images list` use.
func printLogTable(w io.Writer, entries []logs.Entry) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No run logs found (~/.astrona/logs is empty).")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	fmt.Fprintln(tw, "#\tCOMMAND\tLAB\tSTARTED\tDURATION\tRESULT\tSIZE")
	for _, e := range entries {
		dur := e.Duration
		if dur == "" {
			dur = "-"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Index, e.Command, e.Lab,
			e.Started.Local().Format("2006-01-02 15:04"),
			dur, strings.ToUpper(e.Result), formatBytes(e.Size))
	}
	tw.Flush()
}

// --- logs view -----------------------------------------------------------

func newLogsViewCmd() *cobra.Command {
	var lf logFlags
	var format string
	var noPager bool
	var tail int
	var grep string

	cmd := &cobra.Command{
		Use:   "view [ref]",
		Short: "Show a recorded run's log — raw text through your pager, or structured json/yaml",
		Args:  cobra.MaximumNArgs(1),
		Long: "Show a recorded run's log.\n\n" +
			"With no [ref], the most recent run is shown (respecting --lab / --command). " +
			"[ref] may be an index from `astrona logs list`, the UTC stamp in a log's " +
			"filename, or a path to the file.\n\n" +
			"Raw format is streamed through your pager ($PAGER, else `less`); --no-pager, " +
			"a non-terminal stdout, or --tail / --grep write straight to stdout.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			entry, err := logs.Resolve(ref, lf.filter())
			if err != nil {
				return err
			}

			switch format {
			case "", "raw":
				return viewRaw(entry, noPager, tail, grep)
			case "json":
				l, err := logs.Parse(entry)
				if err != nil {
					return err
				}
				return writeJSON(os.Stdout, l)
			case "yaml":
				l, err := logs.Parse(entry)
				if err != nil {
					return err
				}
				return writeYAML(os.Stdout, l)
			default:
				return fmt.Errorf("unknown --format %q (want raw, json, or yaml)", format)
			}
		},
	}

	bindSelect(cmd, &lf)
	cmd.Flags().StringVar(&format, "format", "raw", "Output format: raw, json, yaml")
	cmd.Flags().BoolVar(&noPager, "no-pager", false, "Write straight to stdout instead of paging")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the last N lines (raw format)")
	cmd.Flags().StringVar(&grep, "grep", "", "Show only lines matching this regular expression (raw format)")
	return cmd
}

// viewRaw writes e's log to the pager (or stdout). tail and grep force the
// file to be read line by line and disable paging; without them the file
// bytes are streamed straight through.
func viewRaw(e logs.Entry, noPager bool, tail int, grep string) error {
	var re *regexp.Regexp
	if grep != "" {
		r, err := regexp.Compile(grep)
		if err != nil {
			return fmt.Errorf("bad --grep pattern: %w", err)
		}
		re = r
	}

	filtered := tail > 0 || re != nil

	out, paged, closePager, err := openPager(noPager || filtered)
	if err != nil {
		return err
	}
	defer closePager()

	if !filtered {
		if err := logs.Copy(out, e); err != nil && !paged {
			return err
		}
		return nil
	}

	f, err := os.Open(e.Path)
	if err != nil {
		return fmt.Errorf("open log %s: %w", e.Path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var lines []string
	for sc.Scan() {
		line := sc.Text()
		if re != nil && !re.MatchString(line) {
			continue
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read log %s: %w", e.Path, err)
	}
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return nil
}

// openPager returns a writer for view output. When disabled, or stdout is
// not a terminal, or no pager binary is found, that writer is os.Stdout and
// paged is false. Otherwise it is the stdin of $PAGER (default `less`), and
// the returned closer must be called to flush and reap it.
func openPager(disabled bool) (w io.Writer, paged bool, closer func(), err error) {
	noop := func() {}
	if disabled || !isatty.IsTerminal(os.Stdout.Fd()) {
		return os.Stdout, false, noop, nil
	}

	name := os.Getenv("PAGER")
	if name == "" {
		name = "less"
	}
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return os.Stdout, false, noop, nil
	}

	path, lookErr := exec.LookPath(fields[0])
	if lookErr != nil {
		return os.Stdout, false, noop, nil
	}

	args := fields[1:]
	if filepath.Base(path) == "less" && len(args) == 0 {
		// -R keep color escapes, -F skip the pager for output that fits one
		// screen, -X don't clear the screen on exit.
		args = []string{"-R", "-F", "-X"}
	}

	c := exec.Command(path, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	pipe, pipeErr := c.StdinPipe()
	if pipeErr != nil {
		return nil, false, noop, fmt.Errorf("pipe to pager: %w", pipeErr)
	}
	if startErr := c.Start(); startErr != nil {
		return nil, false, noop, fmt.Errorf("start pager %s: %w", path, startErr)
	}

	closer = func() {
		_ = pipe.Close()
		_ = c.Wait()
	}
	return pipe, true, closer, nil
}

// --- logs path -----------------------------------------------------------

func newLogsPathCmd() *cobra.Command {
	var lf logFlags
	var all bool

	cmd := &cobra.Command{
		Use:          "path [ref]",
		Short:        "Print the absolute path of a recorded run's log file",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				entries, err := logs.List(lf.filter())
				if err != nil {
					return err
				}
				for _, e := range entries {
					fmt.Println(e.Path)
				}
				return nil
			}
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			e, err := logs.Resolve(ref, lf.filter())
			if err != nil {
				return err
			}
			fmt.Println(e.Path)
			return nil
		},
	}

	bindListFilters(cmd, &lf)
	cmd.Flags().BoolVar(&all, "all", false, "Print every matching log path, newest first")
	return cmd
}

// --- logs export -------------------------------------------------------------

func newLogsExportCmd() *cobra.Command {
	var lf logFlags
	var output string
	var format string
	var all bool
	var force bool

	cmd := &cobra.Command{
		Use:   "export [ref]",
		Short: "Copy one or more run logs out of ~/.astrona to a file or directory",
		Args:  cobra.MaximumNArgs(1),
		Long: "Copy run logs out of ~/.astrona.\n\n" +
			"Exporting a single log writes --output as a file (or, if --output is an " +
			"existing directory, a file inside it). --all, or any multi-log filter, " +
			"writes one file per log into the --output directory.\n\n" +
			"--format raw copies the log verbatim; json / yaml write the parsed log " +
			"({meta, lines:[{kind,text}]}).",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output == "" {
				return fmt.Errorf("--output is required")
			}
			switch format {
			case "", "raw", "json", "yaml":
			default:
				return fmt.Errorf("unknown --format %q (want raw, json, or yaml)", format)
			}

			var entries []logs.Entry
			if all {
				es, err := logs.List(lf.filter())
				if err != nil {
					return err
				}
				entries = es
			} else {
				ref := ""
				if len(args) == 1 {
					ref = args[0]
				}
				e, err := logs.Resolve(ref, lf.filter())
				if err != nil {
					return err
				}
				entries = []logs.Entry{e}
			}
			if len(entries) == 0 {
				return fmt.Errorf("no run logs match the given filters")
			}

			return exportEntries(entries, output, format, all, force)
		},
	}

	bindListFilters(cmd, &lf)
	cmd.Flags().StringVarP(&output, "output", "o", "", "Destination file, or directory for multiple logs (required)")
	cmd.Flags().StringVar(&format, "format", "raw", "Exported content format: raw, json, yaml")
	cmd.Flags().BoolVar(&all, "all", false, "Export every matching log into the --output directory")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing destination files")
	return cmd
}

func exportEntries(entries []logs.Entry, output, format string, all, force bool) error {
	multi := all || len(entries) > 1 || isDir(output)
	if multi {
		if err := os.MkdirAll(output, 0o755); err != nil {
			return fmt.Errorf("create export directory %s: %w", output, err)
		}
	}

	for _, e := range entries {
		dest := output
		if multi {
			dest = filepath.Join(output, exportName(e.File, format))
		}
		if !force {
			if _, err := os.Stat(dest); err == nil {
				return fmt.Errorf("%s already exists (use --force to overwrite)", dest)
			}
		}
		if err := writeExport(dest, e, format); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", dest)
	}
	return nil
}

func writeExport(dest string, e logs.Entry, format string) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer f.Close()

	switch format {
	case "", "raw":
		return logs.Copy(f, e)
	case "json":
		l, err := logs.Parse(e)
		if err != nil {
			return err
		}
		return writeJSON(f, l)
	case "yaml":
		l, err := logs.Parse(e)
		if err != nil {
			return err
		}
		return writeYAML(f, l)
	}
	return nil
}

func exportName(file, format string) string {
	switch format {
	case "json":
		return strings.TrimSuffix(file, ".log") + ".json"
	case "yaml":
		return strings.TrimSuffix(file, ".log") + ".yaml"
	default:
		return file
	}
}

// --- logs clean -------------------------------------------------------------

func newLogsCleanCmd() *cobra.Command {
	var lf logFlags
	var olderThan time.Duration
	var keep int
	var dryRun bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Delete old run logs",
		Long: "Delete run logs under ~/.astrona/logs.\n\n" +
			"Without --yes, clean only previews what it would remove. --keep always " +
			"retains the N newest matching logs; --older-than restricts deletion to " +
			"logs older than the given duration.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			doomed, err := logs.Prune(lf.filter(), keep, olderThan, true)
			if err != nil {
				return err
			}
			if len(doomed) == 0 {
				fmt.Println("Nothing to delete.")
				return nil
			}
			for _, e := range doomed {
				fmt.Printf("  %s  %-8s  %s\n",
					e.Started.Local().Format("2006-01-02 15:04"), e.Command, e.File)
			}
			if dryRun {
				fmt.Printf("\n%d log(s) would be deleted (dry run).\n", len(doomed))
				return nil
			}
			if !yes {
				fmt.Printf("\n%d log(s) will be deleted. Re-run with --yes to confirm.\n", len(doomed))
				return nil
			}
			removed, err := logs.Prune(lf.filter(), keep, olderThan, false)
			if err != nil {
				return err
			}
			fmt.Printf("\nDeleted %d log(s).\n", len(removed))
			return nil
		},
	}

	bindSelect(cmd, &lf)
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only delete logs older than this (e.g. 168h)")
	cmd.Flags().IntVar(&keep, "keep", 0, "Always retain the N newest matching logs")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be deleted and stop")
	cmd.Flags().BoolVar(&yes, "yes", false, "Actually delete — without this, clean only previews")
	return cmd
}

// --- shared helpers -------------------------------------------------------------

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

func writeYAML(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode yaml: %w", err)
	}
	return enc.Close()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
