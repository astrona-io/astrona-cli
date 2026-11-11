// Package guestrun keeps the one anonymous report a signed-out ("continue
// as guest") catalog lab or playground run sends once it counts — a lab
// submitted, a playground destroyed — so Astrona's hands-on time includes
// guests. A report carries an anonymous install id, the catalog lab id,
// the kind, the start and finish times, the time used and the CLI version;
// nothing else (no user name, e-mail, host name or IP of its own).
//
// The install id is a random UUID v4 in ~/.astrona/install-id, made the
// first time a guest report needs one and never derived from the hardware.
// `astrona login` and `astrona logout` replace it with a fresh one, so guest
// runs before a sign-in, while signed in and after a sign-out never share
// an id and can't be linked to the account. A report already queued keeps
// the id it was made with.
//
// Reports wait in ~/.astrona/guest-runs, one file each, until the site
// takes them; the offline-signal sender (`astrona outbox-flush`) retries
// them, and every astrona command starts it when something waits.
package guestrun

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/filelock"
)

// MaxAge is how long a report waits before it is given up.
const MaxAge = 7 * 24 * time.Hour

// MaxUsed caps the time one report claims: the site refuses more.
const MaxUsed = 8 * time.Hour

// Kinds of run.
const (
	Lab        = "lab"
	Playground = "playground"
)

// Report is one counted guest run, as POST {site}/api/cli/guest-runs takes
// it. Site is where it goes and is not sent.
type Report struct {
	InstallID      string    `json:"install_id"`
	Kind           string    `json:"kind"`
	Lab            string    `json:"lab"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	UsedSeconds    int       `json:"used_seconds"`
	EndReason      string    `json:"end_reason"`
	IdempotencyKey string    `json:"idempotency_key"`
	CLIVersion     string    `json:"cli_version"`
	Site           string    `json:"-"`
}

// stored is a queued report with its site.
type stored struct {
	Site   string    `json:"site"`
	Queued time.Time `json:"queued"`
	Report Report    `json:"report"`
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func home() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, apphome.Name()), nil
}

// newUUID is a random UUID v4.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// InstallID is the anonymous install id, made (without asking) the first
// time it is needed. An unreadable or malformed one is replaced.
func InstallID() (string, error) {
	dir, err := home()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "install-id")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); uuidV4.MatchString(id) {
			return id, nil
		}
	}
	return writeInstallID(dir)
}

// RotateInstallID replaces the install id with a fresh random one (after a
// sign-in or a sign-out). Reports already queued keep theirs.
func RotateInstallID() error {
	dir, err := home()
	if err != nil {
		return err
	}
	_, err = writeInstallID(dir)
	return err
}

func writeInstallID(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	id := newUUID()
	if err := writeAtomic(filepath.Join(dir, "install-id"), []byte(id+"\n")); err != nil {
		return "", fmt.Errorf("save the install id: %w", err)
	}
	return id, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Dir is ~/.astrona/guest-runs.
func Dir() (string, error) {
	dir, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "guest-runs"), nil
}

func lock(wait bool) (func(), bool, error) {
	dir, err := Dir()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	return filelock.Lock(filepath.Join(dir, "queue.lock"), wait)
}

// NewKey is a fresh idempotency key.
func NewKey() string { return strings.ReplaceAll(newUUID(), "-", "") }

func validKey(key string) bool {
	if len(key) < 16 || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// Queue keeps r until Flush sends it. The same key queued twice is kept
// once.
func Queue(r Report) error {
	if !validKey(r.IdempotencyKey) {
		return fmt.Errorf("invalid guest run key %q", r.IdempotencyKey)
	}
	if r.Site == "" {
		return errors.New("guest run has no site")
	}
	release, _, err := lock(true)
	if err != nil {
		return err
	}
	defer release()
	dir, _ := Dir()
	data, err := json.Marshal(stored{Site: r.Site, Queued: time.Now().UTC(), Report: r})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, r.IdempotencyKey+".json"), data)
}

func files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths, nil
}

// Pending counts the reports waiting.
func Pending() int {
	dir, err := Dir()
	if err != nil {
		return 0
	}
	paths, _ := files(dir)
	return len(paths)
}

// Poster sends one report to site. Permanent (the site refused it for
// good) drops it; any other error keeps it for the next try.
type Poster func(ctx context.Context, site string, r Report) error

// Flush sends what waits; it returns how many are still waiting. permanent
// says whether an error is a refusal for good. When another process is
// flushing it does nothing.
func Flush(ctx context.Context, post Poster, permanent func(error) bool) (int, error) {
	release, ok, err := lock(false)
	if err != nil {
		return 0, err
	}
	if !ok {
		return Pending(), nil
	}
	defer release()
	dir, _ := Dir()
	paths, err := files(dir)
	if err != nil {
		return 0, err
	}
	left := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		var s stored
		if err != nil || json.Unmarshal(data, &s) != nil || s.Site == "" || time.Since(s.Queued) > MaxAge {
			_ = os.Remove(path) // damaged or too old: given up
			continue
		}
		if ctx.Err() != nil {
			left++
			continue
		}
		err = post(ctx, s.Site, s.Report)
		if err == nil || permanent(err) {
			_ = os.Remove(path)
			continue
		}
		left++
	}
	return left, nil
}
