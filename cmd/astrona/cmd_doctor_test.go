package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"

	"github.com/spf13/cobra"
)

func TestDoctorVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	old := Version
	defer func() { Version = old }()
	Version = "v0.2.2"

	for _, c := range []struct {
		constraint string
		status     checkStatus
		detail     string
	}{
		{"", checkOK, "any astrona"},
		{">=0.2.0", checkOK, "fits"},
		{"<=0.2.1", checkFail, "no fitting version is installed"},
		{"~0.2", checkFail, "astronaVersion"},
	} {
		r := doctorVersion(&config.LabConfig{AstronaVersion: c.constraint})
		if r.status != c.status || !strings.Contains(r.detail, c.detail) {
			t.Errorf("%q = %v %q", c.constraint, r.status, r.detail)
		}
	}
	if err := doctorVerdict(&report{}, 0); err != nil {
		t.Errorf("no problems = %v", err)
	}
	if err := doctorVerdict(&report{}, 2); err == nil || !strings.Contains(err.Error(), "2 problem(s)") {
		t.Errorf("problems = %v", err)
	}
}

// A lab that was named but can't be loaded is a ✗, not "no lab here".
func TestDoctorLoadLab(t *testing.T) {
	empty := t.TempDir()
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "astrona.yaml"), []byte("metadata: [not, a, map\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "astrona.yaml"), []byte("metadata:\n  name: net-01\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, dir, configPath, fileName string
		named, wantCfg, wantErr         bool
	}{
		{"no lab here", empty, ".", "astrona.yaml", false, false, false},
		{"named lab missing", empty, filepath.Join(empty, "missing"), "astrona.yaml", true, false, true},
		{"named dir without config", empty, empty, "astrona.yaml", true, false, true},
		{"named . without config", empty, ".", "astrona.yaml", true, false, true},
		{"named --file missing", good, ".", "other.yaml", true, false, true},
		{"malformed config here", bad, ".", "astrona.yaml", false, false, true},
		{"good lab", good, ".", "astrona.yaml", false, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(c.dir)
			cfg, _, err := doctorLoadLab(&rootFlags{configPath: c.configPath, fileName: c.fileName}, c.named)
			if (cfg != nil) != c.wantCfg || (err != nil) != c.wantErr {
				t.Fatalf("cfg=%v err=%v", cfg, err)
			}
			if err != nil {
				if r := doctorLabError(err); r.status != checkFail || r.hint == "" {
					t.Errorf("row = %+v", r)
				}
			}
		})
	}
}

// Naming the lab is about what was given, not where it resolves to.
func TestLabNamed(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		set   map[string]string
		flags rootFlags
		want  bool
	}{
		{"nothing", nil, nil, rootFlags{configPath: "."}, false},
		{"argument .", []string{"."}, nil, rootFlags{configPath: "."}, true},
		{"-c .", nil, map[string]string{"config": "."}, rootFlags{configPath: "."}, true},
		{"--file", nil, map[string]string{"file": "other.yaml"}, rootFlags{configPath: "."}, true},
		{"--git", nil, nil, rootFlags{configPath: ".", gitURL: "https://example.com/r.git"}, true},
		{"astrona use", nil, nil, rootFlags{configPath: "/labs/net-01", fromCurrent: true}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			flags := c.flags
			cmd := &cobra.Command{}
			cmd.Flags().StringVarP(&flags.configPath, "config", "c", flags.configPath, "")
			cmd.Flags().StringVar(&flags.fileName, "file", "astrona.yaml", "")
			for k, v := range c.set {
				if err := cmd.Flags().Set(k, v); err != nil {
					t.Fatal(err)
				}
			}
			if got := labNamed(cmd, c.args, &flags); got != c.want {
				t.Errorf("labNamed = %v, want %v", got, c.want)
			}
		})
	}
}
