package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindUnknownFields(t *testing.T) {
	body := []byte(`metadata: {name: x, titel: oops}
runtime:
  kind:
    node: {workers: 1}
    addons: {metricServer: true}
bootstrap:
  waitfor: [{resource: deploy/web}]
  manifest: []
validation:
  checks: [{name: a, type: command, comand: "true"}]
completelyDifferent: 1
`)
	got, err := FindUnknownFields(body)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ in, suggestion string }{
		"titel":               {"metadata", ""}, // nothing close — no misleading guess
		"node":                {"runtime.kind", "nodes"},
		"metricServer":        {"runtime.kind.addons", "metricsServer"},
		"waitfor":             {"bootstrap/testing", "waitFor"},
		"manifest":            {"bootstrap/testing", "manifests"},
		"comand":              {"a validation.checks entry", "command"},
		"completelyDifferent": {"the top level", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d unknown fields, want %d: %v", len(got), len(want), got)
	}
	for _, u := range got {
		w, ok := want[u.Field]
		if !ok {
			t.Errorf("unexpected %+v", u)
			continue
		}
		if u.In != w.in || u.Suggestion != w.suggestion {
			t.Errorf("%s: in=%q suggestion=%q, want in=%q suggestion=%q", u.Field, u.In, u.Suggestion, w.in, w.suggestion)
		}
		if u.Line == 0 {
			t.Errorf("%s: no line number", u.Field)
		}
	}
	if s := got[0].String(); !strings.Contains(s, "line 1") {
		t.Errorf("String() = %q", s)
	}
}

func TestFindUnknownFieldsCleanConfigs(t *testing.T) {
	for _, body := range []string{"", "metadata: {name: x}\n", "runtime:\n  kind:\n    addons: {gatewayAPI: envoy}\n"} {
		if got, err := FindUnknownFields([]byte(body)); err != nil || len(got) != 0 {
			t.Errorf("%q: got %v, %v", body, got, err)
		}
	}

	examples, _ := filepath.Glob("../../examples/*/config.yaml")
	if len(examples) == 0 {
		t.Fatal("no example configs found")
	}
	for _, f := range examples {
		body, _ := os.ReadFile(f)
		if got, err := FindUnknownFields(body); err != nil || len(got) != 0 {
			t.Errorf("%s: %v %v", f, got, err)
		}
	}
}

func TestLoadLabConfigRecordsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("metadata: {name: x}\nbootstrap: {waitfor: []}\n"), 0600)
	cfg, _, err := LoadLabConfig(path)
	if err != nil {
		t.Fatalf("an unknown field must not fail loading (only warn): %v", err)
	}
	if len(cfg.UnknownFields) != 1 || cfg.UnknownFields[0].Suggestion != "waitFor" {
		t.Fatalf("UnknownFields = %+v", cfg.UnknownFields)
	}
}

// The committed schema (published on the docs site) must match the
// structs; regenerate with: go run ./cmd/astrona schema > docs/schema/lab-config.schema.json
func TestSchemaFileUpToDate(t *testing.T) {
	want, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/schema/lab-config.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("docs/schema/lab-config.schema.json is stale — run: go run ./cmd/astrona schema > docs/schema/lab-config.schema.json")
	}
}

func TestSchemaEnumsPointAtRealFields(t *testing.T) {
	types := configTypes()
	for key := range schemaEnums {
		typ, field, _ := strings.Cut(key, ".")
		tt, ok := types[typ]
		if !ok {
			t.Errorf("schemaEnums %q: no type %s reachable from LabConfig", key, typ)
			continue
		}
		found := false
		for _, n := range yamlFieldNames(tt) {
			found = found || n == field
		}
		if !found {
			t.Errorf("schemaEnums %q: %s has no field %q", key, typ, field)
		}
	}
}
