package main

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"astrona/internal/executor"

	"github.com/spf13/cobra"
)

// The first Ctrl+C's notices.
const (
	interruptBuildNotice   = "Interrupted — stopping, then removing what this run created."
	interruptGradeNotice   = "Interrupted — stopping the grading; nothing is recorded or sent."
	interruptDestroyNotice = "Interrupted — carrying on with the rest of the destroy, so less is left behind."
)

// exitInterrupted is the exit code of a second Ctrl+C: 128 + SIGINT, what
// a shell reports for a process killed by it.
const exitInterrupted = 130

// interruptible makes Ctrl+C (SIGINT) and SIGTERM cancel cmd's context
// instead of killing astrona, so a command that builds, grades or removes
// a lab can stop and clean up after itself. The first signal prints notice
// on stderr and cancels the context; a second one quits at once. Commands
// it wraps (and the tools they run through internal/executor, which pass
// the signal on to their own process group) are expected to stop at the
// next step and undo what this run created.
//
// It is armed after a command's questions, never before: a prompt keeps
// Ctrl+C's usual meaning. The returned func disarms it — call it before
// handing the terminal to something that reads Ctrl+C itself (the lab
// workspace, a shell); it gives cmd its earlier context back.
func interruptible(cmd *cobra.Command, notice string) (context.Context, func()) {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stopSignals := cancelOnSignal(func(_ os.Signal, first bool) {
		if !first {
			fmt.Fprintln(os.Stderr, "\nQuitting now — anything left over is removed by `astrona destroy`.")
			os.Exit(exitInterrupted)
		}
		fmt.Fprintf(os.Stderr, "\n%s Press Ctrl+C again to quit now.\n", notice)
	}, os.Interrupt, syscall.SIGTERM)
	// The command's work stops with either the parent or the signal.
	merged, cancel := context.WithCancel(parent)
	stopMerge := context.AfterFunc(ctx, cancel)
	executor.HandleInterrupts(true)
	cmd.SetContext(merged)
	return merged, func() {
		executor.HandleInterrupts(false)
		stopMerge()
		stopSignals()
		cancel()
		cmd.SetContext(parent)
	}
}
