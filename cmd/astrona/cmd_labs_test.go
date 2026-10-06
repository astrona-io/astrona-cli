package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/catalog"
)

func TestLabArgResolvesCatalogNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	store := catalog.Store{Dir: filepath.Join(home, ".astrona")}
	store.Save(catalog.Catalog{FetchedAt: time.Now(), Trainings: []catalog.Training{{
		ID: "ATS014", Repo: "https://github.com/astrona-io/ATS014.git",
		Labs: []catalog.Lab{{ID: "ATS014/section-010/module-01/lab-01", Path: "sections/section-010/module-01/labs/lab-01"}},
	}}})

	f := &rootFlags{configPath: ".", fileName: "config.yaml"}
	if err := labArg([]string{"ats014/section-010/module-01/lab-01"}, f); err != nil {
		t.Fatal(err)
	}
	if f.gitURL != "https://github.com/astrona-io/ATS014.git" || f.configPath != "sections/section-010/module-01/labs/lab-01" {
		t.Errorf("resolved to git %q config %q", f.gitURL, f.configPath)
	}

	if err := labArg([]string{"ATS014/section-999/module-01/lab-01"}, &rootFlags{}); err == nil || !strings.Contains(err.Error(), "astrona labs ATS014") {
		t.Errorf("unknown lab = %v", err)
	}

	// A directory that exists wins over a catalog name.
	os.MkdirAll(filepath.Join("ATS014", "local"), 0755)
	g := &rootFlags{configPath: ".", fileName: "config.yaml"}
	if err := labArg([]string{"ATS014/local"}, g); err != nil || g.gitURL != "" || g.configPath != "ATS014/local" {
		t.Errorf("local path = git %q config %q, %v", g.gitURL, g.configPath, err)
	}
}
