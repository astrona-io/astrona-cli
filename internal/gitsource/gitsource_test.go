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
}
