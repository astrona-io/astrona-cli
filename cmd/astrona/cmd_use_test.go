package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestUseLabArg(t *testing.T) {
	cases := []struct {
		arg                 string
		gitExplicit         bool
		wantConfig, wantGit string
		wantFile            string
	}{
		{"./labs/web", false, "./labs/web", "", "config.yaml"},
		{"./labs/web/lab.yaml", false, "labs/web", "", "lab.yaml"},
		{"https://example.com/lab/config.yaml", false, "https://example.com/lab/config.yaml", "", "config.yaml"},
		{"https://github.com/org/labs", false, ".", "https://github.com/org/labs", "config.yaml"},
		{"git@github.com:org/labs.git", false, ".", "git@github.com:org/labs.git", "config.yaml"},
		{"labs/net-01", true, "labs/net-01", "https://github.com/org/labs", "config.yaml"},
	}
	for _, c := range cases {
		f := &rootFlags{configPath: ".", fileName: "config.yaml", gitExplicit: c.gitExplicit}
		if c.gitExplicit {
			f.gitURL = "https://github.com/org/labs"
		}
		useLabArg([]string{c.arg}, f)
		if f.configPath != c.wantConfig || f.gitURL != c.wantGit || f.fileName != c.wantFile {
			t.Errorf("%q (git given %v) = config %q git %q file %q", c.arg, c.gitExplicit, f.configPath, f.gitURL, f.fileName)
		}
	}
}

func TestCurrentLabPrecedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := &currentLab{Config: "/labs/remembered", Name: "remembered"}
	if err := saveCurrentLab(saved); err != nil {
		t.Fatal(err)
	}
	newCmd := func() (*cobra.Command, *rootFlags) {
		f := &rootFlags{configPath: ".", fileName: "config.yaml"}
		c := &cobra.Command{Use: "x"}
		c.Flags().StringVarP(&f.configPath, "config", "c", ".", "")
		c.Flags().StringVar(&f.gitURL, "git", "", "")
		c.Flags().StringVarP(&f.fileName, "file", "f", "config.yaml", "")
		return c, f
	}

	// No lab named, none in the current directory: the remembered one.
	t.Chdir(t.TempDir())
	c, f := newCmd()
	applyCurrentLab(c, f)
	if f.configPath != "/labs/remembered" {
		t.Errorf("remembered lab not used: %q", f.configPath)
	}

	// -c given: it wins.
	c, f = newCmd()
	c.Flags().Set("config", "./mine")
	applyCurrentLab(c, f)
	if f.configPath != "./mine" {
		t.Errorf("-c lost to the remembered lab: %q", f.configPath)
	}

	// Inside a lab directory: that lab wins.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("metadata: {name: here}\n"), 0600)
	t.Chdir(dir)
	c, f = newCmd()
	applyCurrentLab(c, f)
	if f.configPath != "." {
		t.Errorf("lab in the current directory lost to the remembered lab: %q", f.configPath)
	}

	if err := saveCurrentLab(nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadCurrentLab(); got != nil {
		t.Errorf("cleared lab still there: %+v", got)
	}
}
