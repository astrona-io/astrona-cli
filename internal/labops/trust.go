package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"astrona/internal/account"
	"astrona/internal/apphome"
	"astrona/internal/catalog"
	"astrona/internal/config"
	"astrona/internal/resources"
	"astrona/internal/trust"
)

// TrustSource identifies where cfg came from, pinned to its exact version —
// nil for a local config (the user's own files, never prompted).
func TrustSource(src LabSource, cfg *config.LabConfig, baseDir string) (*trust.Source, error) {
	if src.Git != "" {
		out, err := exec.Command("git", "-C", baseDir, "rev-parse", "HEAD").Output()
		if err != nil {
			return nil, fmt.Errorf("could not read the lab's git commit: %w", err)
		}
		loc := src.Git
		if src.GitRef != "" {
			loc += "@" + src.GitRef
		}
		if sub := strings.Trim(src.Config, "./"); sub != "" {
			loc += " (" + sub + ")"
		}
		return &trust.Source{Kind: "git", Location: loc, Pin: strings.TrimSpace(string(out))}, nil
	}
	if strings.HasPrefix(cfg.SourcePath, "https://") || strings.HasPrefix(cfg.SourcePath, "http://") {
		return &trust.Source{Kind: "url", Location: cfg.SourcePath, Pin: "sha256:" + cfg.SourceSHA256}, nil
	}
	return nil, nil
}

// RiskSummary lists everything a lab does that reaches beyond its own
// cluster: what runs on the host (scripts, command checks), what's
// fetched at run time (and so not covered by the approval), and what it
// installs or exposes.
func RiskSummary(cfg *config.LabConfig) []string {
	where := "on this machine (bash)"
	if cfg.Runtime.Type == "qemu" {
		where = "inside the lab VM(s)"
	}
	var lines []string
	var fetched []string
	for _, ref := range ResourceRefs(cfg) {
		if strings.Contains(ref.Where, "manifests") {
			continue
		}
		lines = append(lines, fmt.Sprintf("runs %s: %s", where, describeSource(ref.Item)))
		if strings.EqualFold(ref.Item.Type, "url") {
			fetched = append(fetched, ref.Item.Source)
		}
	}
	// Every check that executes or connects from this machine, wherever
	// it's configured — kubectl checks only read the lab's own cluster.
	checks := cfg.Validation.Checks
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil {
			checks = append(checks[:len(checks):len(checks)], vm.Validation.Checks...)
		}
	}
	for _, c := range checks {
		switch strings.ToLower(c.Type) {
		case "command":
			lines = append(lines, "runs on this machine (command check): "+c.Command)
		case "http":
			lines = append(lines, "requests from this machine (http check): "+c.URL)
		}
	}
	nManifests := 0
	for _, ref := range ResourceRefs(cfg) {
		if strings.Contains(ref.Where, "manifests") {
			nManifests++
			if strings.EqualFold(ref.Item.Type, "url") {
				fetched = append(fetched, ref.Item.Source)
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
		lines = append(lines, "creates a certificate authority for the lab and installs it in its clusters (key kept in ~/"+apphome.Name()+" and in the clusters)")
	}
	for _, l := range cfg.KindClusters() {
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
	// Every line carries the lab's own strings (names, URLs, commands):
	// shown before the user approves it, they mustn't be able to rewrite
	// the prompt.
	for i := range lines {
		lines[i] = SanitizeLine(lines[i])
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

// ResourceRiskLines are the trust prompt's lines about the lab's resources:
// they're its code too, but nothing runs until the student asks.
func ResourceRiskLines(cfg *config.LabConfig, baseDir string) []string {
	list, err := resources.Collect(cfg, baseDir)
	if err != nil {
		return []string{SanitizeLine("⚠ its resources can't be read: " + err.Error())}
	}
	if len(list) == 0 {
		return nil
	}
	where := "inside the isolated lab terminal (--host explicitly runs on this machine)"
	if cfg.Runtime.Type == "qemu" {
		where = "inside the lab VM"
	}
	parts := make([]string, len(list))
	for i, r := range list {
		how := r.Command()
		if how == "" {
			how = "file, never run"
		}
		parts[i] = r.Name + " (" + how + ")"
	}
	return []string{SanitizeLine(fmt.Sprintf("ships %d resource(s), copied to ~/%s/resources — each runs %s only when you ask (astrona res run): %s",
		len(list), apphome.Name(), where, strings.Join(parts, ", ")))}
}

// ErrNotTrusted is RequireTrust's error when the user answers no.
var ErrNotTrusted = errors.New("not trusted — nothing was run")

// Approver asks the user whether to run a lab they haven't approved at
// this version — status says whether it is new or changed since prevPin —
// after showing what it does (summary). cmd's is the terminal prompt; a
// browser workspace can ask its own way.
type Approver func(src trust.Source, status trust.Status, prevPin string, summary []string) bool

// TrustOptions are the caller's trust choices.
type TrustOptions struct {
	// Explicit is --trust: approve without asking (CI).
	Explicit bool
	// Approve asks the user. nil: nobody to ask (no terminal, --headless)
	// — an unapproved lab is refused.
	Approve Approver
	// Log receives the "Trusting … (--trust)." line and the official-lab
	// line (OfficialTrust).
	Log io.Writer
	// Official looks up the commit Astrona publishes for an official
	// training repository. nil: no lab is pre-trusted as official.
	Official PublishedPin

	// official is set by RequireTrust for the source being checked.
	official func(trust.Source) bool
}

// PublishedPin returns the commit Astrona publishes for the official
// training repository repo (its name under catalog.DefaultOrg) and the
// site it was read from; an error when there is none.
type PublishedPin func(repo string) (pin, site string, err error)

// AstronaPublishedPin is the CLI's PublishedPin: the "sourceSha" of the
// training in the lab catalog of astrona.io, read over HTTPS when asked.
// Another site (ASTRONA_URL, a saved sign-in elsewhere) never vouches for
// official labs — except in an unversioned development build, so a local
// stack can be tested.
func AstronaPublishedPin(repo string) (string, string, error) {
	client, _, site, err := AccountDeps()
	if err != nil {
		return "", "", err
	}
	if !account.IsDefaultSite(site) && !apphome.Development() {
		return "", "", fmt.Errorf("official pins are only read from %s, not %s", account.DefaultSite, site)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pins, err := client.OfficialLabPins(ctx, site)
	if err != nil {
		return "", "", err
	}
	pin, ok := pins[strings.ToLower(repo)]
	if !ok {
		return "", "", fmt.Errorf("%s publishes no pin for %s", site, repo)
	}
	return pin, site, nil
}

// OfficialTrust reports whether tsrc is an official catalog lab whose
// content is exactly what Astrona published, so it runs without the
// trust prompt. All of it must hold, else the normal prompt follows:
//   - it was picked from the catalog as an official training's lab
//     (src.Catalog) and is a git source;
//   - its repository is github.com/<catalog.DefaultOrg>/<repo>;
//   - the checkout's commit (tsrc.Pin) equals the commit Astrona publishes
//     for that repository (opts.Official) — a different ref, a newer push
//     not yet synced, or no published pin all mismatch;
//   - the checkout has no local changes.
//
// Nothing is recorded in the trust store: the next run checks again.
func OfficialTrust(src LabSource, tsrc trust.Source, baseDir string, opts TrustOptions) bool {
	if opts.Official == nil || src.Catalog == "" || tsrc.Kind != "git" {
		return false
	}
	repo, ok := catalog.OfficialRepo(src.Git)
	if !ok {
		return false
	}
	say := func(format string, a ...any) {
		if opts.Log != nil {
			fmt.Fprintln(opts.Log, SanitizeLine(fmt.Sprintf(format, a...)))
		}
	}
	pin, site, err := opts.Official(repo)
	if err != nil {
		say("Not pre-trusted as an official lab: %s.", err)
		return false
	}
	if !strings.EqualFold(pin, tsrc.Pin) {
		say("Not pre-trusted as an official lab: %s is at %s, %s published %s.", repo, ShortPin(tsrc.Pin), site, ShortPin(pin))
		return false
	}
	out, err := exec.Command("git", "-C", baseDir, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil || len(strings.TrimSpace(string(out))) > 0 {
		say("Not pre-trusted as an official lab: its checkout has local changes.")
		return false
	}
	say("Trusted as an official Astrona lab at %s (published by %s).", tsrc.Pin, site)
	return true
}

// RequireTrust stops a remote lab from running anything until the user
// has approved this exact version of it — once per version. Explicit
// approves without asking; an official lab at the commit Astrona
// publishes runs without asking (OfficialTrust); otherwise, without an
// Approver it refuses.
func RequireTrust(src LabSource, cfg *config.LabConfig, baseDir string, opts TrustOptions) error {
	tsrc, err := TrustSource(src, cfg, baseDir)
	if err != nil || tsrc == nil {
		return err
	}
	opts.official = func(ts trust.Source) bool { return OfficialTrust(src, ts, baseDir, opts) }
	return ApproveSource(*tsrc, opts, func() []string {
		return append(RiskSummary(cfg), ResourceRiskLines(cfg, baseDir)...)
	})
}

// ApproveSource resolves prompt consent separately from config inspection
// and persisted trust.
func ApproveSource(src trust.Source, opts TrustOptions, summary func() []string) error {
	status, prevPin, err := trust.Check(src)
	if err != nil {
		return err
	}
	if status == trust.Trusted {
		return nil
	}
	if opts.Explicit {
		fmt.Fprintf(opts.Log, "Trusting %s at %s (--trust).\n", src.Location, ShortPin(src.Pin))
		return trust.Approve(src, time.Now())
	}
	if opts.official != nil && opts.official(src) {
		return nil
	}
	if opts.Approve == nil {
		return fmt.Errorf("lab %s (%s) hasn't been approved to run on this machine — review it, then pass --trust", src.Location, ShortPin(src.Pin))
	}
	if !opts.Approve(src, status, prevPin, summary()) {
		return ErrNotTrusted
	}
	return trust.Approve(src, time.Now())
}

// RequireTrustUnparsed is RequireTrust for a remote lab this astrona can't
// parse (it was written for another version), before handing it over.
// With no config to show what the lab does, there is no prompt: it goes
// ahead only when this exact version is already approved, or Explicit.
func RequireTrustUnparsed(src LabSource, pe *config.ParseError, baseDir string, opts TrustOptions) error {
	tsrc, err := TrustSource(src, &config.LabConfig{SourcePath: pe.Path, SourceSHA256: pe.SHA256}, baseDir)
	if err != nil || tsrc == nil {
		return err
	}
	status, _, err := trust.Check(*tsrc)
	if err != nil {
		return err
	}
	if status == trust.Trusted {
		return nil
	}
	if opts.Explicit {
		fmt.Fprintf(opts.Log, "Trusting %s at %s (--trust).\n", tsrc.Location, ShortPin(tsrc.Pin))
		return trust.Approve(*tsrc, time.Now())
	}
	if OfficialTrust(src, *tsrc, baseDir, opts) {
		return nil
	}
	return fmt.Errorf("lab %s (%s) hasn't been approved to run on this machine, and this astrona can't read its config to show what it does — review it, then pass --trust", tsrc.Location, ShortPin(tsrc.Pin))
}

// HandoverApproval is the trust check the version gate runs before handing
// a lab to another astrona: RequireTrust when cfg loaded, otherwise
// RequireTrustUnparsed for the config loadErr couldn't parse.
func HandoverApproval(src LabSource, cfg *config.LabConfig, loadErr error, baseDir string, opts TrustOptions) func() error {
	return func() error {
		if cfg != nil {
			return RequireTrust(src, cfg, baseDir, opts)
		}
		var pe *config.ParseError
		if !errors.As(loadErr, &pe) {
			return fmt.Errorf("can't check whether this lab is trusted: %w", loadErr)
		}
		return RequireTrustUnparsed(src, pe, baseDir, opts)
	}
}

// ShortPin is a source pin cut to 12 characters (keeping a "sha256:"
// prefix), as the trust prompt and errors show it.
func ShortPin(pin string) string {
	p := strings.TrimPrefix(pin, "sha256:")
	if len(p) > 12 {
		p = p[:12]
	}
	if strings.HasPrefix(pin, "sha256:") {
		return "sha256:" + p
	}
	return p
}
