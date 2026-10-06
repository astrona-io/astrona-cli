package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestPrintHome(t *testing.T) {
	now := time.Now()
	var buf bytes.Buffer
	printHome(&buf, homeLab{}, false, now)
	if !strings.Contains(buf.String(), "No lab picked") || !strings.Contains(buf.String(), "astrona use <lab") {
		t.Errorf("no lab:\n%s", buf.String())
	}

	buf.Reset()
	printHome(&buf, homeLab{name: "home-test", cluster: "astro-home-test-not-running", source: "astrona use"}, true, now)
	for _, want := range []string{"Lab home-test", "(from astrona use)", "State     not running", "Next: astrona run"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
}

func TestHomeLabFor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("metadata: {name: here-lab}\n"), 0600)
	t.Chdir(dir)
	cmd := &cobra.Command{Use: "astrona"}
	f := &rootFlags{configPath: ".", fileName: "config.yaml"}
	cmd.Flags().StringVarP(&f.configPath, "config", "c", ".", "")
	cmd.Flags().StringVar(&f.gitURL, "git", "", "")
	lab, ok := homeLabFor(cmd, f)
	if !ok || lab.name != "here-lab" || !strings.HasPrefix(lab.source, "this directory") {
		t.Errorf("lab in the current directory = %+v, %v", lab, ok)
	}
}

// Plain `astrona` shows the lab's status and then every command, the same
// list `astrona --help` prints.
func TestBareAstronaListsEveryCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ASTRONA_NO_UPDATE_CHECK", "1")
	t.Chdir(t.TempDir())

	run := func(args ...string) string {
		root := newRootCmd(&rootFlags{})
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("astrona %v: %v", args, err)
		}
		return buf.String()
	}

	bare, help := run(), run("--help")
	if !strings.Contains(bare, "No lab picked") {
		t.Errorf("bare astrona lacks the status block:\n%s", bare)
	}
	if !strings.HasSuffix(bare, help) {
		t.Errorf("bare astrona doesn't end with the --help output:\n%s", bare)
	}
	for _, c := range newRootCmd(&rootFlags{}).Commands() {
		if c.IsAvailableCommand() && !strings.Contains(bare, "  "+c.Name()+" ") {
			t.Errorf("bare astrona doesn't list %q", c.Name())
		}
	}
}
