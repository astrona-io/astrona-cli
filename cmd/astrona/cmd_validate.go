package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"astrona/internal/config"
	"astrona/internal/lifecycle"
	"astrona/internal/resources"
	"astrona/internal/scripts"

	"github.com/spf13/cobra"
)

// labConfigResults checks a loaded lab config without creating anything:
// unknown fields (typos), the semantic validation `run` does, check types,
// and that every local script/manifest/doc it references exists. baseDir
// is "" for a config fetched from a URL — file checks are skipped then.
func labConfigResults(cfg *config.LabConfig, baseDir string) []checkResult {
	var res []checkResult
	fail := func(name, detail string) {
		res = append(res, checkResult{status: checkFail, name: name, detail: detail})
	}

	for _, u := range cfg.UnknownFields {
		r := checkResult{status: checkFail, name: fmt.Sprintf("line %d", u.Line), detail: fmt.Sprintf("unknown field %q in %s", u.Field, u.In)}
		if u.Suggestion != "" {
			r.hint = fmt.Sprintf("did you mean %q?", u.Suggestion)
		}
		res = append(res, r)
	}

	for _, d := range cfg.Deprecations {
		res = append(res, checkResult{status: checkWarn, name: "deprecated", detail: d})
	}
	if cfg.Metadata.Name == "" {
		fail("metadata.name", "missing — the lab would be named 'astrona-lab'")
	}
	if err := lifecycle.Validate(cfg); err != nil {
		fail("runtime / gates", err.Error())
	}
	for i, c := range cfg.Validation.Checks {
		known := false
		for _, t := range config.CheckTypes {
			known = known || strings.EqualFold(t, c.Type)
		}
		if !known {
			fail(fmt.Sprintf("validation.checks[%d]", i), fmt.Sprintf("unsupported type '%s' (%s)", c.Type, strings.Join(config.CheckTypes, ", ")))
		}
	}

	if baseDir == "" {
		res = append(res, checkResult{status: checkWarn, name: "sources", detail: "config loaded from a URL — local file references not checked"})
		return res
	}
	for _, ref := range resourceRefs(cfg) {
		if _, err := scripts.ResolveLocalSource(ref.item, baseDir); err != nil {
			fail(ref.where, err.Error())
		}
	}
	if list, err := resources.Collect(cfg, baseDir); err != nil {
		fail("resources", err.Error())
	} else if len(list) > 0 {
		names := make([]string, len(list))
		for i, r := range list {
			names[i] = r.Name
		}
		res = append(res, checkResult{name: "resources", detail: fmt.Sprintf("%d in %s/: %s", len(list), config.ResourcesDir, strings.Join(names, ", "))})
	}
	docs := docRefs(cfg)
	for _, where := range sortedKeysOf(docs) {
		path := docs[where]
		full, err := config.JoinStrictlyWithinBaseDir(baseDir, path)
		if err != nil {
			res = append(res, checkResult{status: checkWarn, name: where, detail: err.Error()})
			continue
		}
		if _, err := os.Stat(full); err != nil {
			res = append(res, checkResult{status: checkWarn, name: where, detail: fmt.Sprintf("'%s' not found", path)})
		}
	}

	if len(res) == 0 {
		res = append(res, checkResult{name: "lab config", detail: "valid"})
	}
	return res
}

type resourceRef struct {
	where string
	item  config.ResourceItem
}

// resourceRefs lists every script/manifest entry in cfg with where it's
// configured.
func resourceRefs(cfg *config.LabConfig) []resourceRef {
	var refs []resourceRef
	add := func(where string, items []config.ResourceItem) {
		for i, it := range items {
			refs = append(refs, resourceRef{fmt.Sprintf("%s[%d] %s", where, i, it.Name), it})
		}
	}
	addBlock := func(prefix string, b config.BootstrapConfig) {
		add(prefix+".init", b.Init)
		add(prefix+".manifests", b.Manifests)
	}
	addValidation := func(prefix string, v config.ValidationConfig) {
		if v.Script != nil {
			add(prefix+".script", []config.ResourceItem{*v.Script})
		}
		add(prefix+".scripts", v.Scripts)
	}

	addBlock("bootstrap", cfg.Bootstrap)
	addBlock("testing", cfg.Testing)
	add("teardown.init", cfg.Teardown.Init)
	addValidation("validation", cfg.Validation)
	for _, l := range cfg.KindClusters() {
		addBlock("runtime.kind.clusters["+l.Name+"].bootstrap", l.Bootstrap)
		addBlock("runtime.kind.clusters["+l.Name+"].testing", l.Testing)
		add("runtime.kind.clusters["+l.Name+"].teardown.init", l.Teardown.Init)
	}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Bootstrap != nil {
			addBlock("runtime.qemu["+vm.Name+"].bootstrap", *vm.Bootstrap)
		}
		if vm.Validation != nil {
			addValidation("runtime.qemu["+vm.Name+"].validation", *vm.Validation)
		}
	}
	return refs
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

func newValidateCmd(flags *rootFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "validate [lab]",
		Short: "Check a lab config for mistakes without running anything",
		Long: "Check a lab config (-c/--file/--git) without creating anything: unknown fields (typos " +
			"like `waitfor` that would otherwise be silently ignored, with a did-you-mean), runtime/" +
			"addon/port-forward/waitFor rules, validation check types, and that every local " +
			"script, manifest and doc file it references exists.\n\n" +
			"Exits non-zero on any ✗ — suitable as a CI step for lab repositories. For editor " +
			"autocompletion, see the JSON Schema in the lab config reference.",
		Example: `  astrona validate -c ./labs/my-lab
  astrona validate --git https://github.com/org/labs --config labs/lab-01`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := labArg(args, flags); err != nil { // a lab given as the argument wins over `astrona use`
				return err
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			rep := &report{json: output == "json"}
			finalPath, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, flags.verbose)
			if err != nil {
				return rep.loadFailed(flags.configPath, withNoLabHint(err, flags))
			}
			cfg, cleanup, err := config.LoadLabConfig(finalPath)
			if err != nil {
				if want := labVersionFromLoadError(err); want != "" {
					if verr := ensureLabVersion(want, flags, handoverApproval(flags, nil, err, filepath.Dir(finalPath))); verr != nil {
						return rep.loadFailed(finalPath, verr)
					}
				}
				return rep.loadFailed(finalPath, withNoLabHint(err, flags))
			}
			if err := ensureLabVersion(cfg.AstronaVersion, flags, handoverApproval(flags, cfg, nil, filepath.Dir(finalPath))); err != nil {
				cleanup()
				return rep.loadFailed(finalPath, err)
			}
			defer cleanup()

			baseDir := ""
			if !strings.HasPrefix(finalPath, "http://") && !strings.HasPrefix(finalPath, "https://") {
				baseDir = filepath.Dir(finalPath)
			}

			failed := rep.section(finalPath, labConfigResults(cfg, baseDir))
			if rep.json {
				return rep.done(fmt.Errorf("%d problem(s) in the lab config", failed))
			}
			if failed > 0 {
				return fmt.Errorf("%d problem(s) in the lab config", failed)
			}
			fmt.Println("\nLab config is valid.")
			return nil
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

func sortedKeysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
