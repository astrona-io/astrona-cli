package gitsource

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

	for _, step := range []string{"Cloning", "Updating"} {
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
