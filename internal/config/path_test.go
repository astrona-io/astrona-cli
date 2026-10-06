package config

import (
	"path/filepath"
	"testing"
)

func TestJoinStrictlyWithinBaseDir(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		source string
		ok     bool
	}{
		{"docs/guide.md", true},
		{"./a/../b.md", true},
		{"/etc/passwd", false},
		{"../outside.md", false},
		{"a/../../outside.md", false},
	}
	for _, c := range cases {
		got, err := JoinStrictlyWithinBaseDir(base, c.source)
		if c.ok {
			if err != nil {
				t.Errorf("%q: unexpected error: %v", c.source, err)
			} else if want := filepath.Join(base, c.source); got != want {
				t.Errorf("%q: got %s, want %s", c.source, got, want)
			}
		} else if err == nil {
			t.Errorf("%q: got %s, want an error", c.source, got)
		}
	}

	// The lenient variant keeps passing absolute paths through for lab configs.
	if got, err := JoinWithinBaseDir(base, "/etc/passwd"); err != nil || got != "/etc/passwd" {
		t.Errorf("JoinWithinBaseDir absolute = %q, %v; want pass-through", got, err)
	}
}
