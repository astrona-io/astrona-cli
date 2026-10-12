// Package terms records acceptance of the usage notice shipped with the CLI.
package terms

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Revision identifies the notice. The digest also detects edits
// that accidentally leave this revision unchanged.
const Revision = "2026-10-12"

//go:embed notice.txt
var document string

// Document returns the complete notice without a network request.
func Document() string { return document }

// Digest identifies the exact notice and its revision, not the CLI release.
func Digest() string {
	sum := sha256.Sum256([]byte(Revision + "\n" + document))
	return hex.EncodeToString(sum[:])
}

// Store keeps the receipt separate from preferences and credentials.
type Store struct{ Dir string }

// Receipt is a local acknowledgement, not a verified identity or server audit.
type Receipt struct {
	Revision   string    `json:"revision"`
	SHA256     string    `json:"sha256"`
	AcceptedAt time.Time `json:"acceptedAt"`
	Method     string    `json:"method"`
	CLIVersion string    `json:"cliVersion"`
}

// DefaultStore uses the same directory as the CLI's other local state.
func DefaultStore() (Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, fmt.Errorf("find terms directory: %w", err)
	}
	return Store{Dir: filepath.Join(home, ".astrona")}, nil
}

func (s Store) open() (*os.Root, error) {
	info, err := os.Lstat(s.Dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("terms directory %s must be a directory, not a symlink", s.Dir)
	}
	r, err := os.OpenRoot(s.Dir)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, fmt.Errorf("terms directory changed while opening; try again")
	}
	return r, nil
}

// Accepted returns false for missing, malformed or outdated receipts. Unsafe
// paths and read errors stop execution instead of silently replacing a file.
func (s Store) Accepted() (bool, error) {
	r, err := s.open()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open terms directory: %w", err)
	}
	defer r.Close()
	info, err := regularReceipt(r)
	if err != nil || info == nil {
		return false, err
	}
	f, err := r.Open("terms.json")
	if err != nil {
		return false, fmt.Errorf("read terms receipt: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return false, fmt.Errorf("terms receipt changed while opening; try again")
	}
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return false, fmt.Errorf("read terms receipt: %w", err)
	}
	var saved Receipt
	if len(data) > 8192 || json.Unmarshal(data, &saved) != nil {
		return false, nil
	}
	return saved.Revision == Revision && saved.SHA256 == Digest() &&
		!saved.AcceptedAt.IsZero() && (saved.Method == "prompt" || saved.Method == "flag"), nil
}

func regularReceipt(r *os.Root) (os.FileInfo, error) {
	info, err := r.Lstat("terms.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect terms receipt: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("terms.json must be a regular file, not a directory or symlink")
	}
	return info, nil
}

// Accept atomically replaces the receipt. Concurrent acceptances can replace
// each other, but readers always see a complete receipt for one exact notice.
func (s Store) Accept(method, cliVersion string) error {
	if method != "prompt" && method != "flag" {
		return fmt.Errorf("invalid terms acceptance method %q", method)
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create terms directory: %w", err)
	}
	r, err := s.open()
	if err != nil {
		return fmt.Errorf("open terms directory: %w", err)
	}
	defer r.Close()
	if _, err := regularReceipt(r); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Receipt{Revision, Digest(), time.Now().UTC(), method, cliVersion}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode terms receipt: %w", err)
	}
	name := ".terms-" + rand.Text() + ".tmp"
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create terms receipt: %w", err)
	}
	defer r.Remove(name)
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write terms receipt: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync terms receipt: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close terms receipt: %w", err)
	}
	if err := r.Rename(name, "terms.json"); err != nil {
		return fmt.Errorf("save terms receipt: %w", err)
	}
	return nil
}
