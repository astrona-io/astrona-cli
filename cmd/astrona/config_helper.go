package main

import (
	"os"

	"astrona/internal/config"
	"astrona/internal/labops"

	"github.com/mattn/go-isatty"
)

// withNoLabHint adds what to do when no lab was named and the current
// directory has none — the usual first stumble.
func withNoLabHint(err error, flags *rootFlags) error {
	return labops.WithNoLabHint(err, labOpsSource(flags))
}

// labVersionFromLoadError is the astronaVersion of a config this version
// couldn't parse, if it declares one.
func labVersionFromLoadError(err error) string { return labops.LabVersionFromLoadError(err) }

// versionGate is the CLI's labops.VersionGate: ensureLabVersion with the
// command's flags, gated as a local-only run when localOnly says so.
func versionGate(flags *rootFlags) labops.VersionGate {
	return func(constraint string, localOnly bool, approve func() error) error {
		versionFlags := *flags
		versionFlags.localOnly = localOnly
		return ensureLabVersion(constraint, &versionFlags, approve)
	}
}

// LoadLabForCommand resolves --config/--file (and --git/--git-ref, if set),
// loads the YAML, and returns the lab's base directory for resolving
// relative script/manifest paths (labops.LoadLab). Shared by every command
// that requires a valid config to do anything (run, submit, test) —
// cmd_destroy.go does NOT use this, since destroy must still best-effort
// tear down even when the config can't be loaded.
func LoadLabForCommand(flags *rootFlags) (cfg *config.LabConfig, baseDir string, cleanup func(), err error) {
	lab, err := labops.LoadLab(labOpsSource(flags), labops.LoadOptions{
		Version:     Version,
		Verbose:     flags.verbose,
		LocalOnly:   flags.localOnly,
		VersionGate: versionGate(flags),
		Trust:       trustOptions(flags),
		// Say which lab when it came from `astrona use` — it isn't on the
		// command line. Terminal only, so $(…) captures stay clean.
		AnnounceLab: flags.fromCurrent && isatty.IsTerminal(os.Stderr.Fd()),
		Log:         os.Stderr,
	})
	if err != nil {
		return nil, "", func() {}, err
	}
	return lab.Config, lab.BaseDir, lab.Cleanup, nil
}

// requireRunningKindLab fails clearly when a kind lab isn't running
// (labops.RequireRunningKindLab).
func requireRunningKindLab(cfg *config.LabConfig, clusterName string) error {
	return labops.RequireRunningKindLab(cfg, clusterName)
}
