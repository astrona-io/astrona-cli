package gitsource

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/apphome"
)

// captureStdio runs fn with os.Stdout and os.Stderr redirected and returns
// what each received.
func captureStdio(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	read := func(f **os.File) func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := *f
		*f = w
		done := make(chan string)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		return func() string {
			_ = w.Close()
			*f = old
			return <-done
		}
	}
	stopOut, stopErr := read(&os.Stdout), read(&os.Stderr)
	fn()
	return stopOut(), stopErr()
}

func TestGitCacheSeparatesURLAndRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	a, b := filepath.Join(root, "repo@branch"), filepath.Join(root, "repo")
	for _, repo := range []string{a, b} {
		if err := os.Mkdir(repo, 0700); err != nil {
			t.Fatal(err)
		}
		fixtureGit(t, repo, "init", "-q", "--initial-branch=main")
		commitFixture(t, repo, repo)
		fixtureGit(t, repo, "branch", "branch@")
	}
	first, err := ResolveGitConfigSource(a, "", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveGitConfigSource(b, "branch@", false)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different repository/ref pairs shared a checkout")
	}
	if got, err := os.ReadFile(filepath.Join(second, "config.yaml")); err != nil || string(got) != b {
		t.Fatalf("requested repository content = %q, %v", got, err)
	}
}

func TestLegacyGitCacheChecksOrigin(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "colliding"}[matching], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			src, _, _ := newGitFixture(t)
			cache, err := apphome.ReposDir()
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte(src + "@"))
			legacy := filepath.Join(cache, hex.EncodeToString(sum[:])[:16])
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			refreshFixture(t, src, "", legacy)
			if !matching {
				fixtureGit(t, legacy, "remote", "set-url", "origin", "https://other.example/repo.git")
			}
			got, err := CachedGitConfigSource(src, "")
			if err != nil {
				t.Fatal(err)
			}
			if (got == legacy) != matching {
				t.Fatalf("legacy reuse: got %s, matching=%v", got, matching)
			}
			if matching {
				if err := os.Rename(src, src+"-offline"); err != nil {
					t.Fatal(err)
				}
			}
			updated, err := ResolveGitConfigSource(src, "", false)
			if err != nil || updated != got {
				t.Fatalf("resolve cache = %q, %v", updated, err)
			}
			if !matching && fixtureGit(t, legacy, "remote", "get-url", "origin") != "https://other.example/repo.git" {
				t.Fatal("mismatched legacy cache was changed")
			}
		})
	}
}

func TestGitCacheRefusesChangedOriginBeforeCleaning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	src, _, _ := newGitFixture(t)
	dir, err := ResolveGitConfigSource(src, "", false)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "notes")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, dir, "remote", "set-url", "origin", "https://other.example/repo.git")
	for _, resolve := range []func() (string, error){
		func() (string, error) { return CachedGitConfigSource(src, "") },
		func() (string, error) { return ResolveGitConfigSource(src, "", false) },
		func() (string, error) { return dir, cloneOrUpdateGitRepo(src, "", dir, false) },
	} {
		if _, err := resolve(); err == nil || !strings.Contains(err.Error(), "expected") {
			t.Fatalf("mismatched origin accepted: %v", err)
		}
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("mismatched checkout was cleaned: %q, %v", got, err)
	}
}

func TestGitCacheRelativeSourceOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	src, _, _ := newGitFixture(t)
	t.Chdir(filepath.Dir(src))
	first, err := ResolveGitConfigSource("./origin", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ResolveGitConfigSource("./origin", "", false); err != nil || second != first {
		t.Fatalf("relative origin reuse = %q, %v", second, err)
	}
}

func TestGitCacheRetriesFailedFirstClone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	src, _, _ := newGitFixture(t)
	offline := src + "-offline"
	if err := os.Rename(src, offline); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveGitConfigSource(src, "", false); err == nil {
		t.Fatal("unavailable first clone succeeded")
	}
	if err := os.Rename(offline, src); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveGitConfigSource(src, "", false); err != nil {
		t.Fatalf("first clone could not be retried: %v", err)
	}
}

// Clone/update progress — ours and, with verbose, git's — must stay off
// stdout, which is the command's output (`astrona labs -o json`).
func TestCloneOrUpdateKeepsStdoutClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	dest := filepath.Join(t.TempDir(), "checkout")

	for _, step := range []string{"Cloning", "Using cached"} {
		var err error
		stdout, stderr := captureStdio(t, func() {
			err = cloneOrUpdateGitRepo(src, "", dest, true)
		})
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if stdout != "" {
			t.Errorf("%s wrote to stdout: %q", step, stdout)
		}
		if !strings.Contains(stderr, step+" "+src) {
			t.Errorf("%s: stderr = %q", step, stderr)
		}
	}

	// A tag ref still checks out with the trailing "--" in place.
	if out, err := exec.Command("git", "-C", src, "tag", "v1").CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}
	var err error
	captureStdio(t, func() { err = cloneOrUpdateGitRepo(src, "v1", dest, false) })
	if err != nil {
		t.Fatalf("checkout tag v1: %v", err)
	}
}

func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFixture(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, dir, "add", "config.yaml")
	fixtureGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-qm", content)
	return fixtureGit(t, dir, "rev-parse", "HEAD")
}

func newGitFixture(t *testing.T) (src, dest, revision string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	src, dest = filepath.Join(root, "origin"), filepath.Join(root, "cache")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, src, "init", "-q", "--initial-branch=main")
	return src, dest, commitFixture(t, src, "first")
}

func refreshFixture(t *testing.T, src, ref, dest string) string {
	t.Helper()
	var err error
	stdout, stderr := captureStdio(t, func() {
		err = cloneOrUpdateGitRepo(src, ref, dest, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Fatalf("git progress leaked to stdout: %q", stdout)
	}
	return stderr
}

func TestGitCacheFreshness(t *testing.T) {
	for _, ref := range []string{"", "main", "refs/heads/main", "v1", "refs/tags/v1", "annotated", "commit"} {
		t.Run("ref="+ref, func(t *testing.T) {
			src, dest, first := newGitFixture(t)
			fixtureGit(t, src, "tag", "v1")
			fixtureGit(t, src, "-c", "user.name=t", "-c", "user.email=t@example.com", "tag", "-a", "annotated", "-m", "first")
			selectedRef := ref
			if ref == "commit" {
				selectedRef = first
			}
			refreshFixture(t, src, selectedRef, dest)
			fetchHead := filepath.Join(dest, ".git", "FETCH_HEAD")
			if err := os.WriteFile(fetchHead, []byte("no fetch needed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			message := refreshFixture(t, src, selectedRef, dest)
			if !strings.Contains(message, "Using cached") || strings.Contains(message, "Updating") {
				t.Fatalf("unchanged checkout: %s", message)
			}
			if data, err := os.ReadFile(fetchHead); err != nil || string(data) != "no fetch needed\n" {
				t.Fatalf("unchanged repository was fetched: %q, %v", data, err)
			}

			second := commitFixture(t, src, "second")
			fixtureGit(t, src, "tag", "-f", "v1")
			fixtureGit(t, src, "-c", "user.name=t", "-c", "user.email=t@example.com", "tag", "-fa", "annotated", "-m", "second")
			message = refreshFixture(t, src, selectedRef, dest)
			want := second
			if ref == "commit" {
				want = first
			} else if !strings.Contains(message, "Updating") {
				t.Fatalf("changed repository was not fetched: %s", message)
			}
			if got := fixtureGit(t, dest, "rev-parse", "HEAD"); got != want {
				t.Fatalf("revision = %s, want %s", got, want)
			}
			// The next metadata check must also skip a download after an update.
			if message := refreshFixture(t, src, selectedRef, dest); !strings.Contains(message, "Using cached") {
				t.Fatalf("updated cache not reused: %s", message)
			}
			if err := os.Rename(src, src+"-offline"); err != nil {
				t.Fatal(err)
			}
			message = refreshFixture(t, src, selectedRef, dest)
			if ref != "commit" && !strings.Contains(message, "using the cached checkout") {
				t.Fatalf("offline fallback did not warn: %s", message)
			}
			if ref == "commit" && !strings.Contains(message, "Using cached") {
				t.Fatalf("cached commit should not need the remote: %s", message)
			}
			if got := fixtureGit(t, dest, "rev-parse", "HEAD"); got != want {
				t.Fatalf("offline revision = %s, want %s", got, want)
			}
		})
	}
}

func TestUnchangedGitCacheStillCleansLocalChanges(t *testing.T) {
	src, dest, first := newGitFixture(t)
	refreshFixture(t, src, "", dest)
	for _, file := range []string{"config.yaml", "untracked", ".gitignore", "ignored"} {
		if err := os.WriteFile(filepath.Join(dest, file), []byte("ignored\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, dest, "add", "config.yaml")
	if message := refreshFixture(t, src, "", dest); !strings.Contains(message, "Using cached") {
		t.Fatalf("expected cache reuse: %s", message)
	}
	if got := fixtureGit(t, dest, "status", "--porcelain", "--ignored"); got != "" {
		t.Fatalf("cache was not cleaned: %s", got)
	}
	if got := fixtureGit(t, dest, "rev-parse", "HEAD"); got != first {
		t.Fatalf("wrong cached revision: %s", got)
	}
}

func TestGitCacheFollowsChangedDefaultBranch(t *testing.T) {
	src, dest, _ := newGitFixture(t)
	refreshFixture(t, src, "", dest)
	fixtureGit(t, src, "checkout", "-qb", "next")
	want := commitFixture(t, src, "new default")
	refreshFixture(t, src, "", dest)
	if got := fixtureGit(t, dest, "rev-parse", "HEAD"); got != want {
		t.Fatalf("did not follow remote HEAD: %s, want %s", got, want)
	}
}

func TestLegacyGitCacheWithoutRevisionMetadata(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "online", true: "offline"}[offline], func(t *testing.T) {
			src, dest, want := newGitFixture(t)
			refreshFixture(t, src, "", dest)
			fixtureGit(t, dest, "update-ref", "-d", cachedRevisionRef)
			if offline {
				if err := os.Rename(src, src+"-offline"); err != nil {
					t.Fatal(err)
				}
			}
			refreshFixture(t, src, "", dest)
			if got := fixtureGit(t, dest, "rev-parse", cachedRevisionRef); got != want {
				t.Fatalf("legacy cache not recorded: %s, want %s", got, want)
			}
		})
	}
}

func TestGitCacheFetchFailurePreservesCachedRevision(t *testing.T) {
	src, dest, first := newGitFixture(t)
	refreshFixture(t, src, "", dest)
	commitFixture(t, src, "second")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$3\" = fetch ]; then exit 128; fi\nexec \"$ASTRONA_TEST_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASTRONA_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	message := refreshFixture(t, src, "", dest)
	if !strings.Contains(message, "Updating") || !strings.Contains(message, "using the cached checkout") {
		t.Fatalf("expected fetch failure warning: %s", message)
	}
	for _, ref := range []string{"HEAD", cachedRevisionRef} {
		if got := fixtureGit(t, dest, "rev-parse", ref); got != first {
			t.Fatalf("failed fetch changed %s: %s, want %s", ref, got, first)
		}
	}
}

func TestGitCachePrefersBranchOverTag(t *testing.T) {
	src, dest, _ := newGitFixture(t)
	fixtureGit(t, src, "tag", "release")
	fixtureGit(t, src, "checkout", "-qb", "release")
	commitFixture(t, src, "branch")
	refreshFixture(t, src, "release", dest)
	want := commitFixture(t, src, "branch update")
	refreshFixture(t, src, "release", dest)
	if got := fixtureGit(t, dest, "rev-parse", "HEAD"); got != want {
		t.Fatalf("selected tag instead of branch: %s, want %s", got, want)
	}
}

// A URL or ref starting with "-" would reach git as an option
// (--upload-pack=…), so it's rejected before git ever runs.
func TestResolveGitConfigSourceRejectsLeadingDash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct{ url, ref, want string }{
		{"--upload-pack=touch /tmp/pwned", "", "invalid git URL"},
		{"-u", "", "invalid git URL"},
		{"https://example.com/lab.git", "--output=/tmp/x", "invalid git ref"},
		{"https://example.com/lab.git", "-b", "invalid git ref"},
	}
	for _, c := range cases {
		_, err := ResolveGitConfigSource(c.url, c.ref, false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("ResolveGitConfigSource(%q, %q) = %v, want error containing %q", c.url, c.ref, err, c.want)
		}
	}
}
