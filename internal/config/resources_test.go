package config

import (
	"strings"
	"testing"
)

func TestValidateResources(t *testing.T) {
	qemu := RuntimeConfig{Type: "qemu", QEMU: []QEMUVM{{Name: "jump"}}}
	for _, c := range []struct {
		name    string
		runtime RuntimeConfig
		res     []LabResource
		want    string // "" = valid
	}{
		{"none", RuntimeConfig{}, nil, ""},
		{"file and run", RuntimeConfig{}, []LabResource{{File: "setup.sh"}, {File: "tool", Run: "go run ."}, {File: "app.yaml", Type: "file"}}, ""},
		{"nested", RuntimeConfig{}, []LabResource{{File: "tool/main.go"}}, ""},
		{"qemu vm", qemu, []LabResource{{File: "x.sh", VM: "jump"}}, ""},
		{"no file", RuntimeConfig{}, []LabResource{{Description: "x"}}, "file is required"},
		{"absolute", RuntimeConfig{}, []LabResource{{File: "/etc/passwd"}}, "inside resources/"},
		{"escapes", RuntimeConfig{}, []LabResource{{File: "../config.yaml"}}, "inside resources/"},
		{"backslash", RuntimeConfig{}, []LabResource{{File: `a\b.sh`}}, "inside resources/"},
		{"twice", RuntimeConfig{}, []LabResource{{File: "a.sh"}, {File: "./A.sh"}}, "listed twice"},
		{"bad type", RuntimeConfig{}, []LabResource{{File: "a.sh", Type: "script"}}, "unsupported type"},
		{"file with run", RuntimeConfig{}, []LabResource{{File: "a.sh", Type: "file", Run: "bash a.sh"}}, "never run"},
		{"vm on kind", RuntimeConfig{}, []LabResource{{File: "a.sh", VM: "jump"}}, "only for qemu"},
		{"unknown vm", qemu, []LabResource{{File: "a.sh", VM: "nope"}}, "no VM named"},
	} {
		err := ValidateResources(&LabConfig{Runtime: c.runtime, Resources: c.res})
		if c.want == "" && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
