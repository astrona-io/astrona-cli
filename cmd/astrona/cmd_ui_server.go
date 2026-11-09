package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/apphome"
	"astrona/internal/workspace"
)

// defaultWorkspacePort is the port the browser workspace prefers on
// 127.0.0.1; a free one is used when it is taken.
const defaultWorkspacePort = 47300

// newUIServeCmd is the browser workspace server. astrona run starts it
// detached; it is not meant to be run by hand.
func newUIServeCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:    "serve",
		Short:  "Serve the browser workspace on 127.0.0.1 (started by astrona run)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serveWorkspace(cmd.Context(), port)
		},
	}
	cmd.Flags().IntVar(&port, "port", defaultWorkspacePort, "Preferred port on 127.0.0.1; a free port is used when it is taken")
	return cmd
}

func serveWorkspace(parent context.Context, port int) error {
	if parent == nil {
		parent = context.Background()
	}
	logger := log.New(os.Stderr, "", log.LstdFlags|log.LUTC)
	if info, err := workspace.RunningServer(); err == nil && info.PID != os.Getpid() {
		return fmt.Errorf("a workspace server is already running (pid %d, port %d)", info.PID, info.Port)
	}
	srv, err := workspace.New(workspace.Config{
		Port: port, Version: Version, Profile: apphome.Name(), IdleTimeout: workspace.IdleTimeout,
	})
	if err != nil {
		return err
	}
	path, err := workspace.ServerInfoPath()
	if err != nil {
		srv.Close()
		return err
	}
	if err := workspace.WriteServerInfo(path, srv.Info()); err != nil {
		srv.Close()
		return err
	}
	defer workspace.RemoveServerInfo(path, os.Getpid())
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	logger.Printf("workspace server %s listening on 127.0.0.1:%d (pid %d)", Version, srv.Port(), os.Getpid())
	err = srv.Serve(ctx)
	switch {
	case errors.Is(err, workspace.ErrIdle):
		logger.Printf("idle for %s; stopped", workspace.IdleTimeout)
		return nil
	case err != nil:
		logger.Printf("stopped: %v", err)
		return err
	}
	logger.Printf("stopped")
	return nil
}

type uiStatusView struct {
	Running   bool                     `json:"running"`
	PID       int                      `json:"pid,omitempty"`
	Port      int                      `json:"port,omitempty"`
	StartedAt *time.Time               `json:"startedAt,omitempty"`
	Version   string                   `json:"version,omitempty"`
	URL       string                   `json:"url,omitempty"`
	Clients   int                      `json:"clients"`
	Terminals []workspace.TerminalInfo `json:"terminals"`
}

func newUIStatusCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the browser workspace server runs, with a fresh link",
		Long: "Show whether the browser workspace server is running, its open browser connections and terminals, " +
			"and a fresh single-use link to open it (valid for two minutes). Read-only: it never starts the server.",
		Example: "  astrona ui status\n  astrona ui status -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(output, "text"); err != nil {
				return err
			}
			view, err := uiStatus(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if output == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(view)
			}
			if !view.Running {
				_, err := fmt.Fprintln(out, "The browser workspace is not running. astrona run <lab> starts it when you choose to open a lab in your browser.")
				return err
			}
			fmt.Fprintf(out, "Browser workspace: running on 127.0.0.1:%d (pid %d)\n", view.Port, view.PID)
			fmt.Fprintf(out, "Open: %s\n", view.URL)
			fmt.Fprintf(out, "Browser connections: %d\n", view.Clients)
			if len(view.Terminals) == 0 {
				_, err := fmt.Fprintln(out, "Terminals: none")
				return err
			}
			fmt.Fprintln(out, "Terminals:")
			for _, t := range view.Terminals {
				attached := ""
				if t.Attached {
					attached = ", attached"
				}
				fmt.Fprintf(out, "  %s on %s (%s%s)\n", t.Lab, t.Target, t.Status, attached)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "Output format: text or json")
	return cmd
}

func uiStatus(ctx context.Context) (uiStatusView, error) {
	view := uiStatusView{Terminals: []workspace.TerminalInfo{}}
	info, err := workspace.RunningServer()
	if errors.Is(err, workspace.ErrServerNotRunning) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	st, err := info.Status(ctx)
	if err != nil {
		return view, err
	}
	link, err := info.LaunchURL(workspace.DefaultCodeTTL)
	if err != nil {
		return view, err
	}
	started := st.StartedAt
	view.Running, view.PID, view.Port, view.StartedAt, view.Version = true, st.PID, st.Port, &started, st.Version
	view.URL, view.Clients = link, st.Clients
	view.Terminals = append(view.Terminals, st.Terminals...)
	return view, nil
}

func newUIStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the browser workspace server and its terminals; labs keep running",
		Long: "Stop the browser workspace server. Every browser terminal is closed and its container removed. " +
			"Labs, their exam clocks and the files in each lab's terminal workspace are kept; " +
			"stop or destroy a lab with astrona stop or astrona destroy.",
		Example: "  astrona ui stop",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			info, err := workspace.RunningServer()
			if errors.Is(err, workspace.ErrServerNotRunning) {
				_, err := fmt.Fprintln(out, "The browser workspace is not running.")
				return err
			}
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if err := info.Stop(ctx); err != nil {
				// The control route failed; ask the process directly. It
				// handles SIGTERM like stop and closes its terminals.
				if p, findErr := os.FindProcess(info.PID); findErr == nil {
					_ = p.Signal(syscall.SIGTERM)
				}
			}
			deadline := time.Now().Add(20 * time.Second)
			for info.Alive() && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if info.Alive() {
				return fmt.Errorf("the workspace server (pid %d) did not stop within 20s", info.PID)
			}
			_, err = fmt.Fprintln(out, "Browser workspace stopped. Labs keep running.")
			return err
		},
	}
}

// launchWorkspaceURL adds the lab to a launch URL's fragment, so the page
// opens that run.
func launchWorkspaceURL(base, lab string) string {
	if lab == "" {
		return base
	}
	return base + "&lab=" + url.QueryEscape(lab)
}
