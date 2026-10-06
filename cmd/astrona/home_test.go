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
