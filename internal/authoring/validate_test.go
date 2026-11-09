package authoring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/preflight"
)

func TestCheckConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.sh"), []byte("true\n"), 0600)
	cfg := &config.LabConfig{
		Metadata:      config.MetadataConfig{Name: "x", Docs: config.DocsConfig{Solution: "guide.md"}},
		Bootstrap:     config.BootstrapConfig{Init: []config.ResourceItem{{Name: "ok", Type: "file", Source: "ok.sh"}, {Name: "gone", Type: "file", Source: "gone.sh"}}},
		Teardown:      config.TeardownConfig{Init: []config.ResourceItem{{Name: "escape", Type: "file", Source: "../../etc/passwd"}}},
		Validation:    config.ValidationConfig{Checks: []config.ValidationCheck{{Type: "podReady"}, {Type: "httpGet"}}},
		UnknownFields: []config.UnknownField{{Line: 3, Field: "waitfor", In: "bootstrap/testing", Suggestion: "waitFor"}},
	}

	byName := map[string]preflight.Result{}
	for _, r := range CheckConfig(cfg, dir) {
		byName[r.Name] = r
	}
	expect := map[string]preflight.Status{
		"line 3":                  preflight.Fail,
		"validation.checks[1]":    preflight.Fail,
		"bootstrap.init[1] gone":  preflight.Fail,
		"teardown.init[0] escape": preflight.Fail,
		"metadata.docs.solution":  preflight.Warn,
	}
	for name, status := range expect {
		r, ok := byName[name]
		if !ok || r.Status != status {
			t.Errorf("%s: got %+v, want status %d", name, r, status)
		}
	}
	if _, ok := byName["bootstrap.init[0] ok"]; ok {
		t.Error("existing file reported")
	}
	if !strings.Contains(byName["line 3"].Hint, `"waitFor"`) {
		t.Errorf("hint = %q", byName["line 3"].Hint)
	}

	clean := &config.LabConfig{Metadata: config.MetadataConfig{Name: "x"},
		Validation: config.ValidationConfig{Checks: []config.ValidationCheck{{Name: "ok", Type: "command", Command: "true"}}}}
	if res := CheckConfig(clean, dir); len(res) != 1 || res[0].Status != preflight.OK {
		t.Errorf("clean config = %+v", res)
	}
	if res := CheckConfig(clean, ""); res[0].Status != preflight.Warn {
		t.Errorf("URL config should warn that sources weren't checked: %+v", res)
	}
}
