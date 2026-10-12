package labstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/apphome"
)

func TestFinishedRunHistorySurvivesRemoval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	started := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	st := &State{StartedAt: started.Format(time.RFC3339), Source: &Source{Catalog: "ATS/x/playground", Git: "https://secret@example.test/repo"}, Session: &Session{ID: "secret-session", Kind: "playground"}}
	if err := Save("astro-play", st); err != nil {
		t.Fatal(err)
	}
	if err := RecordFinishedRun("astro-play", st, started.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := RecordFinishedRun("astro-play", st, started.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := Remove("astro-play"); err != nil {
		t.Fatal(err)
	}
	runs, err := FinishedRuns()
	if err != nil || len(runs) != 1 || runs[0].Kind != "Playground" || !runs[0].FinishedAt.Equal(started.Add(time.Hour)) {
		t.Fatalf("history: %+v, %v", runs, err)
	}
	dir, _ := runHistoryDir()
	entries, _ := os.ReadDir(dir)
	path := filepath.Join(dir, entries[0].Name())
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "secret") {
		t.Fatal("credential-like source or session data leaked into history")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("history file is not private")
	}
	st.StartedAt = started.Add(3 * time.Hour).Format(time.RFC3339)
	if err := RecordFinishedRun("astro-play", st, started.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	runs, err = FinishedRuns()
	if err != nil || len(runs) != 2 || !runs[0].StartedAt.Equal(started.Add(3*time.Hour)) {
		t.Fatalf("separate run missing: %+v, %v", runs, err)
	}
}

func TestFinishedRunHistoryValidationAndProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wasDevelopment := apphome.Development()
	t.Cleanup(func() {
		if wasDevelopment {
			apphome.Configure("developer")
		} else {
			apphome.Configure("v1.0.0")
		}
	})
	st := &State{StartedAt: time.Now().UTC().Format(time.RFC3339), Source: &Source{Config: "lab"}}
	apphome.Configure("developer")
	if err := RecordFinishedRun("../escape", st, time.Now()); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if err := RecordFinishedRun(apphome.TestClusterPrefix()+"lab", st, time.Now()); err != nil {
		t.Fatal(err)
	}
	if runs, err := FinishedRuns(); err != nil || len(runs) != 0 {
		t.Fatalf("authoring tests included: %+v, %v", runs, err)
	}
	if err := RecordFinishedRun("astrodev-lab", st, time.Now()); err != nil {
		t.Fatal(err)
	}
	dir, _ := runHistoryDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.json"), []byte(strings.Repeat("x", 20*1024)), 0600); err != nil {
		t.Fatal(err)
	}
	if runs, err := FinishedRuns(); err != nil || len(runs) != 1 {
		t.Fatalf("corrupt records hid valid history: %+v, %v", runs, err)
	}
	apphome.Configure("v1.0.0")
	if runs, err := FinishedRuns(); err != nil || len(runs) != 0 {
		t.Fatalf("development history leaked into release profile: %+v, %v", runs, err)
	}
}
