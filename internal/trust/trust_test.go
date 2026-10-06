package trust

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrustLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := Source{Kind: "git", Location: "https://github.com/org/labs", Pin: "aaa"}

	if st, _, err := Check(src); err != nil || st != New {
		t.Fatalf("fresh = %v, %v", st, err)
	}
	if err := Approve(src, time.Now()); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := Check(src); st != Trusted {
		t.Fatalf("after approve = %v", st)
	}

	moved := src
	moved.Pin = "bbb"
	st, prev, _ := Check(moved)
	if st != Changed || prev != "aaa" {
		t.Fatalf("new commit = %v (prev %q), want Changed from aaa", st, prev)
	}

	other := Source{Kind: "url", Location: "https://x/config.yaml", Pin: "sha256:1"}
	if st, _, _ := Check(other); st != New {
		t.Fatal("approval leaked to another source")
	}

	info, err := os.Stat(filepath.Join(home, ".astrona", "trust.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("trust store mode: %v %v", info, err)
	}
}

func TestCorruptStoreIsAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".astrona"), 0700)
	os.WriteFile(filepath.Join(home, ".astrona", "trust.json"), []byte("{not json"), 0600)
	if _, _, err := Check(Source{Kind: "git", Location: "x", Pin: "y"}); err == nil {
		t.Fatal("a corrupt trust store must not silently count as approved")
	}
}
