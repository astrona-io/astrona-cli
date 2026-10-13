//go:build darwin || linux

package ui

import (
	"path/filepath"
	"testing"

	"github.com/creack/pty"
)

func TestPlainProgressOnTerminal(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()
	r, err := New("test", "demo", Options{
		Plain: true, Screen: terminal, LogPath: filepath.Join(t.TempDir(), "test.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.plain || r.color || r.spin != nil || r.verbose {
		t.Fatal("plain progress on a terminal enabled animation, styling or verbose output")
	}
}
