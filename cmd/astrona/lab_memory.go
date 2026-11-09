package main

import (
	"os"

	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// labOpsSource is the lab's config as flags name it.
func labOpsSource(flags *rootFlags) labops.LabSource {
	return labops.LabSource{Config: flags.configPath, File: flags.fileName, Git: flags.gitURL, GitRef: flags.gitRef, Catalog: flags.catalogLab}
}

// rememberedSource is where flags point: what `astrona submit` needs to find the
// lab's config again without being told.
func rememberedSource(flags *rootFlags) *labstate.Source {
	return labops.RememberedSource(labOpsSource(flags))
}

// applySource points flags at a remembered lab's config, like
// applyCurrentLab does for `astrona use`.
func applySource(flags *rootFlags, src *labstate.Source) {
	flags.configPath, flags.gitURL, flags.gitRef, flags.catalogLab = src.Config, src.Git, src.GitRef, src.Catalog
	flags.fileName = "config.yaml"
	if src.File != "" {
		flags.fileName = src.File
	}
	flags.fromCurrent = false
}

// rememberLab records, once a lab is up, where its config came from and
// the lab session it runs under. Best effort: a failure is only a warning.
func rememberLab(clusterName string, flags *rootFlags, sess *labstate.Session) {
	if err := labops.RememberLab(clusterName, rememberedSource(flags), flags.localOnly, sess); err != nil {
		ui.Warnf("could not remember this lab (%s) — `astrona submit` needs it named, and results won't reach the lab page", err)
	}
}

// forgetLab removes active lab memory after ending its session; the
// --keep-resources choice is passed down from here.
func forgetLab(clusterName string) {
	labops.ForgetLab(os.Stderr, clusterName, labops.ForgetOptions{KeepResources: keepResources, EndSession: endRunSession})
}

// previousContextLabel is how the context `astrona destroy` restores is
// shown.
func previousContextLabel(sw *labstate.Switched) string { return labops.PreviousContextLabel(sw) }
