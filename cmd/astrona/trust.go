package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/trust"

	"github.com/mattn/go-isatty"
)

// labSource identifies where cfg came from, pinned to its exact version —
// nil for a local config (the user's own files, never prompted).
func labSource(flags *rootFlags, cfg *config.LabConfig, baseDir string) (*trust.Source, error) {
	if flags.gitURL != "" {
		out, err := exec.Command("git", "-C", baseDir, "rev-parse", "HEAD").Output()
		if err != nil {
			return nil, fmt.Errorf("could not read the lab's git commit: %w", err)
		}
		loc := flags.gitURL
		if flags.gitRef != "" {
			loc += "@" + flags.gitRef
		}
		if sub := strings.Trim(flags.configPath, "./"); sub != "" {
			loc += " (" + sub + ")"
		}
		return &trust.Source{Kind: "git", Location: loc, Pin: strings.TrimSpace(string(out))}, nil
	}
	if strings.HasPrefix(cfg.SourcePath, "https://") || strings.HasPrefix(cfg.SourcePath, "http://") {
		return &trust.Source{Kind: "url", Location: cfg.SourcePath, Pin: "sha256:" + cfg.SourceSHA256}, nil
	}
	return nil, nil
}

// labRiskSummary lists everything a lab does that reaches beyond its own
// cluster: what runs on the host (scripts, command checks), what's
// fetched at run time (and so not covered by the approval), and what it
// installs or exposes.
func labRiskSummary(cfg *config.LabConfig) []string {
	where := "on this machine (bash)"
	if cfg.Runtime.Type == "qemu" {
		where = "inside the lab VM(s)"
	}
	var lines []string
	var fetched []string
	for _, ref := range resourceRefs(cfg) {
		if strings.Contains(ref.where, "manifests") {
			continue
		}
		lines = append(lines, fmt.Sprintf("runs %s: %s", where, describeSource(ref.item)))
		if strings.EqualFold(ref.item.Type, "url") {
			fetched = append(fetched, ref.item.Source)
		}
	}
	for _, c := range cfg.Validation.Checks {
		if strings.EqualFold(c.Type, "command") {
			lines = append(lines, "runs on this machine (command check): "+c.Command)
		}
	}
	nManifests := 0
	for _, ref := range resourceRefs(cfg) {
		if strings.Contains(ref.where, "manifests") {
			nManifests++
			if strings.EqualFold(ref.item.Type, "url") {
				fetched = append(fetched, ref.item.Source)
			}
		}
	}
	if nManifests > 0 {
		lines = append(lines, fmt.Sprintf("applies %d manifest source(s) to the lab cluster", nManifests))
	}
	if k := cfg.Runtime.Kind; k != nil {
		var addons []string
		if k.Addons.CNI != "" {
			addons = append(addons, k.Addons.CNI)
		}
		if k.Addons.CertManager {
			addons = append(addons, "cert-manager")
		}
		if k.Addons.MetricsServer {
			addons = append(addons, "metrics-server")
		}
		if k.Addons.GatewayAPI != "" {
			p := k.Addons.EffectiveGatewayPorts()
			addons = append(addons, fmt.Sprintf("gateway (127.0.0.1:%d/%d)", p.HTTP, p.HTTPS))
		}
		if len(addons) > 0 {
			lines = append(lines, "installs addons (pinned, checksum-verified): "+strings.Join(addons, ", "))
		}
		if len(k.PreloadImages) > 0 {
			lines = append(lines, "pulls images: "+strings.Join(k.PreloadImages, ", "))
		}
	}
	if k := cfg.Runtime.Kind; k != nil && k.SharedCA {
		lines = append(lines, "creates a certificate authority for the lab and installs it in its clusters (key kept in ~/.astrona and in the clusters)")
	}
	for _, l := range cfg.KindLabs() {
		line := "creates linked kind cluster '" + l.Name + "'"
		if len(l.PreloadImages) > 0 {
			line += ", pulls images: " + strings.Join(l.PreloadImages, ", ")
		}
		if !l.Addons.IsZero() {
			line += ", installs addons (pinned, checksum-verified)"
		}
		lines = append(lines, line)
	}
	for _, pf := range cfg.Runtime.PortForwards {
		target := pf.Resource
		if pf.Cluster != "" {
			target += " in linked cluster '" + pf.Cluster + "'"
		}
		lines = append(lines, fmt.Sprintf("opens 127.0.0.1:%d → %s", pf.HostPort, target))
	}
	for _, u := range fetched {
		lines = append(lines, "⚠ fetched when it runs, NOT covered by this approval: "+u)
	}
	if len(lines) == 0 {
		lines = append(lines, "no scripts, checks or manifests that reach outside the cluster")
	}
	return lines
}

func describeSource(it config.ResourceItem) string {
	name := it.Name
	if name == "" {
		name = it.Source
	}
	if strings.EqualFold(it.Type, "url") {
		return fmt.Sprintf("%s (%s)", name, it.Source)
	}
	if name != it.Source {
		return fmt.Sprintf("%s (%s)", name, it.Source)
	}
	return name
}

// requireTrust stops a remote lab from running anything until the user
// has approved this exact version of it — once per version. --trust
// approves without asking (CI); without a terminal it refuses.
func requireTrust(flags *rootFlags, cfg *config.LabConfig, baseDir string) error {
	src, err := labSource(flags, cfg, baseDir)
	if err != nil || src == nil {
		return err
	}
	status, prevPin, err := trust.Check(*src)
	if err != nil {
		return err
	}
	if status == trust.Trusted {
		return nil
	}
	if flags.trust {
		fmt.Fprintf(os.Stderr, "Trusting %s at %s (--trust).\n", src.Location, shortPin(src.Pin))
		return trust.Approve(*src, time.Now())
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("lab %s (%s) hasn't been approved to run on this machine — review it, then pass --trust", src.Location, shortPin(src.Pin))
	}
	if !confirmTrust(os.Stdin, os.Stdout, *src, status, prevPin, labRiskSummary(cfg)) {
		return fmt.Errorf("not trusted — nothing was run")
	}
	return trust.Approve(*src, time.Now())
}

func confirmTrust(in io.Reader, out io.Writer, src trust.Source, status trust.Status, prevPin string, summary []string) bool {
	if status == trust.Changed {
		fmt.Fprintf(out, "\nThis lab changed since you approved it (%s → %s):\n", shortPin(prevPin), shortPin(src.Pin))
	} else {
		fmt.Fprintf(out, "\nFirst time running this lab:\n")
	}
	fmt.Fprintf(out, "  %s (%s)\n\nIt will:\n", src.Location, shortPin(src.Pin))
	for _, l := range summary {
		fmt.Fprintf(out, "  • %s\n", l)
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

func shortPin(pin string) string {
	p := strings.TrimPrefix(pin, "sha256:")
	if len(p) > 12 {
		p = p[:12]
	}
	if strings.HasPrefix(pin, "sha256:") {
		return "sha256:" + p
	}
	return p
}
