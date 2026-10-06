package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"my-lab", true},
		{"astro-my-lab", true},
		{"Lab_1.v2", true},
		{"9lives", true},
		{"", false},
		{"..", false},
		{"../x", false},
		{"x/../../Documents", false},
		{"a/b", false},
		{`a\b`, false},
		{"a,b", false},
		{"a..b", false},
		{".hidden", false},
		{"-flag", false},
		{"has space", false},
	}
	for _, c := range cases {
		err := ValidateName(c.name)
		if (err == nil) != c.ok {
			t.Errorf("ValidateName(%q) = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestLoadLabConfigRejectsUnsafeNames(t *testing.T) {
	cases := []struct {
		yaml string
		want string // "" = loads fine
	}{
		{"metadata:\n  name: my-lab\n", ""},
		{"metadata: {}\n", ""},
		{"metadata:\n  name: x/../../../Documents\n", "metadata.name"},
		{"metadata:\n  name: ..\n", "metadata.name"},
		{"metadata:\n  name: lab\nruntime:\n  type: qemu\n  qemu:\n    - name: vm,file=/etc/passwd\n", "runtime.qemu[0].name"},
		{"metadata:\n  name: lab\nruntime:\n  type: qemu\n  qemu:\n    - name: ../vm\n", "runtime.qemu[0].name"},
		{"metadata:\n  name: lab\nruntime:\n  kind:\n    clusters:\n      - name: ../edge\n", "runtime.kind.clusters[0].name"},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(c.yaml), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := LoadLabConfig(path)
		if c.want == "" {
			if err != nil {
				t.Errorf("%q: unexpected error %v", c.yaml, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error = %v, want one naming %s", c.yaml, err, c.want)
		}
	}
}
