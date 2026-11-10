package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"astrona/internal/account"
	"astrona/internal/cluster"
	"astrona/internal/connect"
	"astrona/internal/labops"
	"astrona/internal/preflight"

	"github.com/spf13/cobra"
)

func newConnectCmd() *cobra.Command {
	var install, uninstall, service bool
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Let the Astrona website start labs on this computer",
		Long: "Keep this computer connected to the Astrona site, so \"Start on my machine\" on a lab page " +
			"builds the lab here. Runs until Ctrl+C, reconnecting by itself after a drop.\n\n" +
			"The website can only ask for run, status, submit, reset, destroy, stop and start, each for a " +
			"catalog lab by name; astrona checks both before anything runs. Nothing else travels over the " +
			"connection: no shell commands, files or URLs.\n\n" +
			"--install starts it with your login session from now on (a LaunchAgent on macOS, a systemd " +
			"user service on Linux); --uninstall removes that again. Its log is ~/.astrona/logs/connect.log.\n\n" +
			"Signing this computer out on the website ends the connection and removes the sign-in here.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case install && uninstall:
				return errors.New("choose --install or --uninstall, not both")
			case install:
				return installConnectService()
			case uninstall:
				return uninstallConnectService()
			}
			client, store, site, err := labops.AccountDeps()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return runConnect(ctx, client, store, site, service)
		},
	}
	cmd.Flags().BoolVar(&install, "install", false, "Start astrona connect with your login session from now on")
	cmd.Flags().BoolVar(&uninstall, "uninstall", false, "Stop starting astrona connect with your login session")
	cmd.Flags().BoolVar(&service, "service", false, "Run under the service --install sets up")
	_ = cmd.Flags().MarkHidden("service")
	return cmd
}

func runConnect(ctx context.Context, client *account.Client, store account.Store, site string, service bool) error {
	if _, err := client.Active(ctx, store, site); err != nil {
		if errors.Is(err, account.ErrSignedOut) {
			return fmt.Errorf("you're not signed in to Astrona (%s) — sign in with: astrona login, then run astrona connect again", site)
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	err = connect.Run(ctx, connect.Options{
		Client: client,
		Store:  store,
		Site:   site,
		Hello:  connectHello,
		Run:    childRunner(exe, os.Stdout),
		Log:    os.Stdout,
	})
	if err == nil {
		fmt.Println("Disconnected.")
		return nil
	}
	hint := ""
	switch {
	case errors.Is(err, connect.ErrRevoked), errors.Is(err, account.ErrSignedOut):
		hint = " — sign in again with: astrona login"
	case errors.Is(err, connect.ErrReplaced):
		hint = " — only one astrona connect runs per computer"
	}
	if service {
		// The service manager would only start it again to the same answer:
		// end cleanly and leave the reason in the log.
		fmt.Printf("Stopped: %v%s\n", err, hint)
		return nil
	}
	return fmt.Errorf("%w%s", err, hint)
}

// connectHello is what the website shows about this computer: version,
// platform, container engine and what it can give a lab.
func connectHello(ctx context.Context) connect.Hello {
	h := connect.Hello{CLIVersion: Version, OS: runtime.GOOS, Arch: runtime.GOARCH}
	if engine, err := cluster.DetectContainerEngine(); err == nil {
		h.Engine = engine.Name
	}
	capacity := &connect.Capacity{}
	if _, info := preflight.CheckEngine(); info != nil {
		capacity.CPUs, capacity.MemoryBytes = info.CPUs, info.MemBytes
	}
	if home, err := os.UserHomeDir(); err == nil {
		var st syscall.Statfs_t
		if syscall.Statfs(home, &st) == nil {
			capacity.DiskFreeBytes = int64(st.Bavail) * int64(st.Bsize) //nolint:gosec,unconvert // block counts fit
		}
	}
	if *capacity != (connect.Capacity{}) {
		h.Capacity = capacity
	}
	return h
}

// connectArgs is the astrona command line for a command from the website.
// Each runs without a terminal, so nothing can stop on a question: what
// would be asked is either decided here (--yes) or fails.
func connectArgs(c connect.Command) []string {
	args := []string{c.Command, c.Lab}
	switch c.Command {
	case "run":
		args = append(args, "--yes", "--no-browser", "--session", c.Session.ID)
	case "reset":
		args = append(args, "--yes")
	case "submit":
		args = append(args, "--keep") // the website decides when to destroy it
	}
	return append(args, "--update-check=false")
}

// childRunner runs each command as `astrona <args>` in an empty working
// directory (so a lab name can never be read as a local path), with its
// output on log. Its last line is the error the website shows.
func childRunner(exe string, log io.Writer) connect.Runner {
	return func(ctx context.Context, c connect.Command) error {
		dir, err := os.MkdirTemp("", "astrona-connect-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		last := &lastLine{w: log}
		child := exec.CommandContext(ctx, exe, connectArgs(c)...)
		child.Dir = dir
		child.Stdin = nil
		child.Stdout, child.Stderr = last, last
		child.Env = append(os.Environ(), "ASTRONA_NO_UPDATE_CHECK=1")
		// Ctrl+C on connect: let the command stop and clean up as it would at a terminal.
		child.Cancel = func() error { return child.Process.Signal(os.Interrupt) }
		child.WaitDelay = 2 * time.Minute
		err = child.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return nil
		case c.Command == "submit" && errors.As(err, &exit) && exit.ExitCode() == exitNotPassed:
			return nil // graded: the result is on the website, passed or not
		case last.String() != "":
			return errors.New(last.String())
		default:
			return err
		}
	}
}

// lastLine copies output to w, indented, and keeps its last non-empty line.
type lastLine struct {
	mu      sync.Mutex
	w       io.Writer
	partial []byte
	last    string
}

func (l *lastLine) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.partial = append(l.partial, p...)
	for {
		i := bytes.IndexByte(l.partial, '\n')
		if i < 0 {
			break
		}
		l.line(string(l.partial[:i]))
		l.partial = l.partial[i+1:]
	}
	return len(p), nil
}

func (l *lastLine) line(s string) {
	s = strings.TrimRight(s, "\r")
	if l.w != nil {
		fmt.Fprintf(l.w, "    │ %s\n", s)
	}
	if t := strings.TrimSpace(s); t != "" {
		l.last = strings.TrimPrefix(t, "Error: ")
	}
}

func (l *lastLine) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.partial) > 0 {
		l.line(string(l.partial))
		l.partial = nil
	}
	return l.last
}
