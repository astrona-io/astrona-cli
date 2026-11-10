package authoring

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/lifecycle"
	"astrona/internal/preflight"
	"astrona/internal/resources"
	"astrona/internal/scripts"
)

// ValidateReport is what `astrona validate` found in one lab config: the
// config's path (the section title) and one result per check. cmd prints
// it as text or -o json.
type ValidateReport struct {
	Path    string
	BaseDir string // "" for a config fetched from a URL
	Results []preflight.Result
}

// ValidateConfig runs CheckConfig on a loaded lab config. configPath is
// the resolved path (or URL) it was loaded from; local file references are
// checked against its directory, and skipped for a URL.
func ValidateConfig(cfg *config.LabConfig, configPath string) ValidateReport {
	baseDir := ""
	if !strings.HasPrefix(configPath, "http://") && !strings.HasPrefix(configPath, "https://") {
		baseDir = filepath.Dir(configPath)
	}
	return ValidateReport{Path: configPath, BaseDir: baseDir, Results: CheckConfig(cfg, baseDir)}
}

// CheckConfig checks a loaded lab config without creating anything:
// unknown fields (typos), the semantic validation `run` does, check types,
// and that every local script/manifest/doc it references exists. baseDir
// is "" for a config fetched from a URL — file checks are skipped then.
// It is a preflight.ConfigChecker.
func CheckConfig(cfg *config.LabConfig, baseDir string) []preflight.Result {
	var res []preflight.Result
	fail := func(name, detail string) {
		res = append(res, preflight.Result{Status: preflight.Fail, Name: name, Detail: detail})
	}

	for _, u := range cfg.UnknownFields {
		r := preflight.Result{Status: preflight.Fail, Name: fmt.Sprintf("line %d", u.Line), Detail: fmt.Sprintf("unknown field %q in %s", u.Field, u.In)}
		if u.Suggestion != "" {
			r.Hint = fmt.Sprintf("did you mean %q?", u.Suggestion)
		}
		res = append(res, r)
	}

	for _, d := range cfg.Deprecations {
		res = append(res, preflight.Result{Status: preflight.Warn, Name: "deprecated", Detail: d})
	}
	if cfg.Metadata.Name == "" {
		fail("metadata.name", "missing — the lab would be named 'astrona-lab'")
	}
	if err := lifecycle.Validate(cfg); err != nil {
		fail("runtime / gates", err.Error())
	}
	// A warning, not a failure: an ungraded playground is a valid lab to
	// run — only `submit` and `test` refuse it.
	if !cfg.HasGrading() {
		res = append(res, preflight.Result{Status: preflight.Warn, Name: "validation", Detail: "nothing to grade — no checks and no scripts, so `astrona submit` and `astrona test` exit 1 (fine for a playground)"})
	}
	checkTypes := func(where string, checks []config.ValidationCheck) {
		for i, c := range checks {
			known := false
			for _, t := range config.CheckTypes {
				known = known || strings.EqualFold(t, c.Type)
			}
			if !known {
				fail(fmt.Sprintf("%s.checks[%d]", where, i), fmt.Sprintf("unsupported type '%s' (%s)", c.Type, strings.Join(config.CheckTypes, ", ")))
			}
		}
	}
	checkTypes("validation", cfg.Validation.Checks)
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil {
			checkTypes("runtime.qemu["+vm.Name+"].validation", vm.Validation.Checks)
		}
	}

	if baseDir == "" {
		// No folder to check against, but an absolute local source is
		// wrong wherever the config lives.
		for _, ref := range labops.ResourceRefs(cfg) {
			if t := strings.ToLower(ref.Item.Type); (t == "file" || t == "folder") && filepath.IsAbs(ref.Item.Source) {
				fail(ref.Where, fmt.Sprintf("path '%s' must be relative to the lab directory, not absolute", ref.Item.Source))
			}
		}
		res = append(res, preflight.Result{Status: preflight.Warn, Name: "sources", Detail: "config loaded from a URL — local file references not checked"})
		return res
	}
	for _, ref := range labops.ResourceRefs(cfg) {
		if _, err := scripts.ResolveLocalSource(ref.Item, baseDir); err != nil {
			fail(ref.Where, err.Error())
		}
	}
	// A qemu base image of type file is read from the lab directory too
	// (existence isn't checked: the source may contain {ARCH}).
	for i, vm := range cfg.Runtime.QEMU {
		if strings.EqualFold(vm.Image.Type, "file") && vm.Image.Source != "" {
			if _, err := config.JoinWithinBaseDir(baseDir, vm.Image.Source); err != nil {
				fail(fmt.Sprintf("runtime.qemu[%d].image.source", i), err.Error())
			}
		}
	}
	if list, err := resources.Collect(cfg, baseDir); err != nil {
		fail("resources", err.Error())
	} else if len(list) > 0 {
		names := make([]string, len(list))
		for i, r := range list {
			names[i] = r.Name
		}
		res = append(res, preflight.Result{Name: "resources", Detail: fmt.Sprintf("%d in %s/: %s", len(list), config.ResourcesDir, strings.Join(names, ", "))})
	}
	docs := docRefs(cfg)
	for _, where := range sortedKeys(docs) {
		path := docs[where]
		full, err := config.JoinWithinBaseDir(baseDir, path)
		if err != nil {
			res = append(res, preflight.Result{Status: preflight.Warn, Name: where, Detail: err.Error()})
			continue
		}
		if _, err := os.Stat(full); err != nil {
			res = append(res, preflight.Result{Status: preflight.Warn, Name: where, Detail: fmt.Sprintf("'%s' not found", path)})
		}
	}
	// A docs tab's file is shown in the browser workspace; a missing or
	// oversized one would fail `astrona run`, so it fails here.
	for i, t := range cfg.Tabs {
		if t.Type != config.TabDocs || t.File == "" {
			continue
		}
		where := fmt.Sprintf("tabs[%d].file", i)
		full, err := config.JoinWithinBaseDir(baseDir, t.File)
		if err != nil {
			fail(where, err.Error())
			continue
		}
		info, err := os.Stat(full)
		switch {
		case err != nil:
			fail(where, fmt.Sprintf("'%s' not found", t.File))
		case !info.Mode().IsRegular():
			fail(where, fmt.Sprintf("'%s' is not a file", t.File))
		case info.Size() > config.MaxTabDocBytes:
			fail(where, fmt.Sprintf("'%s' is larger than %d KiB", t.File, config.MaxTabDocBytes>>10))
		}
	}

	if len(res) == 0 {
		res = append(res, preflight.Result{Name: "lab config", Detail: "valid"})
	}
	return res
}

func docRefs(cfg *config.LabConfig) map[string]string {
	refs := map[string]string{}
	d := cfg.Metadata.Docs
	for where, path := range map[string]string{
		"metadata.docs.prerequisites": d.Prerequisites,
		"metadata.docs.question":      d.Question,
		"metadata.docs.caseStudy":     d.CaseStudy,
		"metadata.docs.solution":      d.Solution,
	} {
		if path != "" {
			refs[where] = path
		}
	}
	return refs
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
