package labops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"astrona/internal/config"
	"astrona/internal/gitsource"
	"astrona/internal/labstate"
	"astrona/internal/ui"
	"astrona/internal/version"
)

// VersionGate makes sure this astrona may run a lab whose config says
// astronaVersion: constraint — or hands the command over to one that may
// (and then doesn't return) — after approve, the lab's trust check, has
// passed. localOnly is a local-only run, which is never handed over. cmd
// implements it (ensureLabVersion): it knows the build's version, the
// installed releases and the command line to hand over.
type VersionGate func(constraint string, localOnly bool, approve func() error) error

// LoadOptions are the caller's choices for LoadLab.
type LoadOptions struct {
	// Version is this astrona's version (main.Version). LoadSavedLab
	// refuses a lab whose astronaVersion doesn't allow it; LoadLab leaves
	// the check to VersionGate, which can also hand over.
	Version string
	// Verbose is --verbose: say which config is loaded.
	Verbose bool
	// LocalOnly is a local-only run (--local-only). A lab remembered as
	// local-only is gated as one too.
	LocalOnly bool
	// VersionGate runs before anything else happens to a lab that names an
	// astronaVersion, so a hand-over comes before any lock or side effect.
	// nil: no version check.
	VersionGate VersionGate
	// Trust is the approval a hand-over needs first.
	Trust TrustOptions
	// AnnounceLab names the lab on Log once loaded (it came from `astrona
	// use`, so it isn't on the command line).
	AnnounceLab bool
	// Log receives progress and warnings (stderr in the CLI).
	Log io.Writer
}

// LoadedLab is a lab config loaded for a command.
type LoadedLab struct {
	Config *config.LabConfig
	// BaseDir resolves the lab's relative script/manifest paths.
	BaseDir string
	// Cleanup removes a fetched config's temporary copy (always set).
	Cleanup func()
}

// LabVersionFromLoadError is the astronaVersion of a config this version
// couldn't parse, if it declares one.
func LabVersionFromLoadError(err error) string {
	var pe *config.ParseError
	if errors.As(err, &pe) {
		return pe.AstronaVersion
	}
	return ""
}

// WithNoLabHint adds what to do when no lab was named and the current
// directory has none — the usual first stumble.
func WithNoLabHint(err error, src LabSource) error {
	if errors.Is(err, os.ErrNotExist) && src.Config == "." && src.Git == "" {
		return fmt.Errorf("%w\nNo lab here — name one (`astrona run ./path/to/lab`, -c, --git) or pick one for every command: astrona use <lab>", err)
	}
	return err
}

// LoadLab resolves src (a local dir, --git/--git-ref, a URL), loads the
// YAML, runs the version gate (with its trust check) and returns the lab
// with its base directory. Unknown fields and deprecations are only
// warned about on Log, so a lab that has always worked keeps working;
// `astrona validate` / `astrona check` fail on them.
func LoadLab(src LabSource, opts LoadOptions) (*LoadedLab, error) {
	finalPath, err := config.ResolveConfigPath(src.Config, src.File, src.Git, src.GitRef, opts.Verbose)
	if err != nil {
		return nil, WithNoLabHint(err, src)
	}

	if opts.Verbose {
		fmt.Fprintf(opts.Log, "Loading configuration from: %s\n", finalPath)
	}

	baseDir := filepath.Dir(finalPath)
	cfg, cleanup, err := config.LoadLabConfig(finalPath)
	if err != nil {
		// A config for another astrona may not parse here: hand over first.
		if want := LabVersionFromLoadError(err); want != "" && opts.VersionGate != nil {
			if verr := opts.VersionGate(want, opts.LocalOnly, HandoverApproval(src, nil, err, baseDir, opts.Trust)); verr != nil {
				return nil, verr
			}
		}
		return nil, WithNoLabHint(err, src)
	}
	localOnly := opts.LocalOnly
	if cfg.AstronaVersion != "" {
		st, stateErr := labstate.Load(config.NormalizeClusterName(cfg.Metadata.Name))
		if stateErr != nil {
			cleanup()
			return nil, stateErr
		}
		if st != nil && st.LocalOnly {
			localOnly = true
		}
	}
	if opts.VersionGate != nil {
		if err := opts.VersionGate(cfg.AstronaVersion, localOnly, HandoverApproval(src, cfg, nil, baseDir, opts.Trust)); err != nil {
			cleanup()
			return nil, err
		}
	}
	if opts.AnnounceLab {
		fmt.Fprintln(opts.Log, ui.Paint(opts.Log, "→ lab "+cfg.Metadata.Name+" (from `astrona use`)", ui.Dim))
	}

	for _, u := range cfg.UnknownFields {
		ui.Fwarnf(opts.Log, "%s %s (ignored — run `astrona validate`)", finalPath, u)
	}
	for _, d := range cfg.Deprecations {
		ui.Fwarnf(opts.Log, "%s: %s", finalPath, d)
	}

	ApplyBundleImages(cfg, baseDir)
	return &LoadedLab{Config: cfg, BaseDir: baseDir, Cleanup: cleanup}, nil
}

// LoadSavedLab reloads the config of the running lab name from its saved
// source, locally only: unlike LoadLab it never fetches, resets a git
// checkout or hands over to another astrona — a lab that wants another
// version is refused (when opts.Version is a release). Only opts.Version
// is read.
func LoadSavedLab(name string, src LabSource, opts LoadOptions) (*LoadedLab, error) {
	path := src.Config
	var err error
	if src.Git != "" {
		var repo string
		repo, err = gitsource.CachedGitConfigSource(src.Git, src.GitRef)
		if err != nil {
			return nil, err
		}
		path, err = config.ResolveGitConfigPath(repo, path, src.File)
	} else if strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") {
		return nil, fmt.Errorf("run %s has only a remote config URL; no local copy is available to reuse. The lab is still running", name)
	} else {
		path, err = config.ResolveConfigPath(path, src.File, "", "", false)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot reopen %s from its local files; restore the saved config or cached repository. The lab is still running: %w", name, err)
	}
	cfg, cleanup, err := config.LoadLabConfig(path)
	if err != nil {
		// LoadLabConfig's cleanup is a no-op until it returns a config.
		return nil, err
	}
	if config.NormalizeClusterName(cfg.Metadata.Name) != name {
		cleanup()
		return nil, fmt.Errorf("saved config now names a different lab; refusing to open %s", name)
	}
	if cfg.AstronaVersion != "" {
		constraint, err := version.ParseConstraint(cfg.AstronaVersion)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("astronaVersion: %w", err)
		}
		if current, err := version.Parse(opts.Version); err == nil && !constraint.Allows(current) {
			cleanup()
			return nil, fmt.Errorf("lab %s needs astrona %s; reopen it with a compatible CLI. The lab is still running", name, constraint)
		}
	}
	baseDir := filepath.Dir(path)
	ApplyBundleImages(cfg, baseDir)
	return &LoadedLab{Config: cfg, BaseDir: baseDir, Cleanup: cleanup}, nil
}
