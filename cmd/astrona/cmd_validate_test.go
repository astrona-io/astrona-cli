package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestLabConfigResults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.sh"), []byte("true\n"), 0600)
	cfg := &config.LabConfig{
		Metadata:      config.MetadataConfig{Name: "x", Docs: config.DocsConfig{Guide: "guide.md"}},
		Bootstrap:     config.BootstrapConfig{Init: []config.ResourceItem{{Name: "ok", Type: "file", Source: "ok.sh"}, {Name: "gone", Type: "file", Source: "gone.sh"}}},
		Teardown:      config.TeardownConfig{Init: []config.ResourceItem{{Name: "escape", Type: "file", Source: "../../etc/passwd"}}},
		Validation:    config.ValidationConfig{Checks: []config.ValidationCheck{{Type: "podReady"}, {Type: "httpGet"}}},
		UnknownFields: []config.UnknownField{{Line: 3, Field: "waitfor", In: "bootstrap/testing", Suggestion: "waitFor"}},
	}

	byName := map[string]checkResult{}
	for _, r := range labConfigResults(cfg, dir) {
		byName[r.name] = r
	}
	expect := map[string]checkStatus{
		"line 3":                  checkFail,
		"validation.checks[1]":    checkFail,
		"bootstrap.init[1] gone":  checkFail,
		"teardown.init[0] escape": checkFail,
		"metadata.docs.guide":     checkWarn,
	}
	for name, status := range expect {
		r, ok := byName[name]
		if !ok || r.status != status {
			t.Errorf("%s: got %+v, want status %d", name, r, status)
		}
	}
	if _, ok := byName["bootstrap.init[0] ok"]; ok {
		t.Error("existing file reported")
	}
	if !strings.Contains(byName["line 3"].hint, `"waitFor"`) {
		t.Errorf("hint = %q", byName["line 3"].hint)
	}

	clean := &config.LabConfig{Metadata: config.MetadataConfig{Name: "x"}}
	if res := labConfigResults(clean, dir); len(res) != 1 || res[0].status != checkOK {
		t.Errorf("clean config = %+v", res)
	}
	if res := labConfigResults(clean, ""); res[0].status != checkWarn {
		t.Errorf("URL config should warn that sources weren't checked: %+v", res)
	}
}
