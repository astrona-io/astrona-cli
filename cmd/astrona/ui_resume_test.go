package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"astrona/internal/gitsource"
	"astrona/internal/labstate"
)

func TestUIResumeUsesLocalFilesWithoutGit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	bin := t.TempDir()
	called := filepath.Join(bin, "git-called")
	t.Setenv("GIT_CALLED", called)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf called > \"$GIT_CALLED\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, runtime := range []string{"kind", "qemu"} {
		for _, git := range []bool{false, true} {
			t.Run(runtime+map[bool]string{false: "/local", true: "/cached"}[git], func(t *testing.T) {
				dir := t.TempDir()
				src := &labstate.Source{Config: dir, File: "lab.yaml", Catalog: "ATS000/section-010/module-01/lab-01"}
				if git {
					src.Git, src.GitRef, src.Config = "https://github.com/example/labs.git", runtime, "labs/demo"
					repo, err := gitsource.CachedGitConfigSource(src.Git, src.GitRef)
					if err != nil {
						t.Fatal(err)
					}
					dir = filepath.Join(repo, src.Config)
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				files := map[string]string{"lab.yaml": "metadata: {name: demo, docs: {question: question.md}}\nruntime: {type: " + runtime + "}\n", "question.md": "# Continue here\nKeep your existing work.", "my-work.txt": "unfinished exercise"}
				for name, body := range files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
				st := &labstate.State{Source: src, StartedAt: "2026-10-12T12:00:00Z", Session: &labstate.Session{ID: "existing-session"}}
				if err := labstate.Save("astro-demo", st); err != nil {
					t.Fatal(err)
				}
				flags := &rootFlags{trust: true, installVersion: true}
				for range 2 {
					cfg, base, cleanup, err := loadUILab("astro-demo", st, flags)
					if err != nil {
						t.Fatal(err)
					}
					cleanup()
					if base != dir || cfg.Runtime.Type != runtime || !sameSource(st.Source, rememberedSource(flags)) {
						t.Fatal("resume changed the source or runtime")
					}
					if task := workspaceTask(cfg, base, flags); !strings.Contains(task, "Continue here") {
						t.Fatal(task)
					}
				}
				for name, body := range files {
					data, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil || string(data) != body {
						t.Fatalf("resume changed %s: %v", name, err)
					}
				}
				after, err := labstate.Load("astro-demo")
				if err != nil || !reflect.DeepEqual(after, st) {
					t.Fatal("resume changed session or timing state")
				}
			})
		}
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatal("resume ran git")
	}
}

func TestUIResumeDoesNotFetchMissingSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	for _, src := range []*labstate.Source{{Git: server.URL, Config: "labs/demo"}, {Config: server.URL + "/config.yaml"}, {Config: filepath.Join(t.TempDir(), "missing")}, {Git: server.URL, Config: "../escape"}} {
		_, _, cleanup, err := loadUILab("astro-demo", &labstate.State{Source: src}, &rootFlags{})
		cleanup()
		if err == nil {
			t.Fatal("accepted unavailable or escaping source")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("resume contacted a server")
	}
	repo, err := gitsource.CachedGitConfigSource(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatal("resume created a cache")
	}
}

func TestUIResumeRejectsChangedNameAndVersion(t *testing.T) {
	oldVersion := Version
	Version = "v0.4.0"
	t.Cleanup(func() { Version = oldVersion })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, body := range []string{"metadata: {name: other}\n", "metadata: {name: demo}\nastronaVersion: '>=99.0.0'\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, cleanup, err := loadUILab("astro-demo", &labstate.State{Source: &labstate.Source{Config: dir}}, &rootFlags{installVersion: true, trust: true})
		cleanup()
		if err == nil {
			t.Fatal("accepted changed lab name or incompatible version")
		}
	}
}
