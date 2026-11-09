package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/trust"

	"github.com/mattn/go-isatty"
)

// The trust policy lives in labops (RequireTrust and friends); what stays
// here is reading the choices from flags and the terminal prompt.

// trustOptions are flags' trust choices: --trust, and the terminal prompt
// as the Approver when stdin is a terminal and the run isn't --headless.
// Evaluated when called, as the check it feeds runs.
func trustOptions(flags *rootFlags) labops.TrustOptions {
	opts := labops.TrustOptions{Explicit: flags.trust, Log: os.Stderr}
	if !flags.headless && isatty.IsTerminal(os.Stdin.Fd()) {
		opts.Approve = terminalApprover
	}
	return opts
}

// terminalApprover is the CLI's labops.Approver: the prompt on stderr,
// the answer from stdin.
func terminalApprover(src trust.Source, status trust.Status, prevPin string, summary []string) bool {
	return confirmTrust(os.Stdin, promptOut, src, status, prevPin, summary)
}

// labSource identifies where cfg came from (labops.TrustSource).
func labSource(flags *rootFlags, cfg *config.LabConfig, baseDir string) (*trust.Source, error) {
	return labops.TrustSource(labOpsSource(flags), cfg, baseDir)
}

// requireTrust stops a remote lab from running anything until the user
// has approved this exact version of it (labops.RequireTrust).
func requireTrust(flags *rootFlags, cfg *config.LabConfig, baseDir string) error {
	return labops.RequireTrust(labOpsSource(flags), cfg, baseDir, trustOptions(flags))
}

// handoverApproval is the trust check ensureLabVersion runs before handing
// a lab to another astrona (labops.HandoverApproval).
func handoverApproval(flags *rootFlags, cfg *config.LabConfig, loadErr error, baseDir string) func() error {
	return func() error {
		return labops.HandoverApproval(labOpsSource(flags), cfg, loadErr, baseDir, trustOptions(flags))()
	}
}

func confirmTrust(in io.Reader, out io.Writer, src trust.Source, status trust.Status, prevPin string, summary []string) bool {
	if status == trust.Changed {
		fmt.Fprintf(out, "\nThis lab changed since you approved it (%s → %s):\n", labops.ShortPin(prevPin), labops.ShortPin(src.Pin))
	} else {
		fmt.Fprintf(out, "\nFirst time running this lab:\n")
	}
	fmt.Fprintf(out, "  %s (%s)\n\nIt will:\n", labops.SanitizeLine(src.Location), labops.SanitizeLine(labops.ShortPin(src.Pin)))
	for _, l := range summary {
		fmt.Fprintf(out, "  • %s\n", labops.SanitizeLine(l))
	}
	fmt.Fprintf(out, "\nOnly continue if you trust its author. Run it? [y/N] ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
