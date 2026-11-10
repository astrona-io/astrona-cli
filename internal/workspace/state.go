package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"astrona/internal/apphome"
)

const serverInfoSchema = 1

// ServerInfo is the content of server.json: how the CLI finds a running
// server and mints launch codes for it. It holds the launch-code secret, so
// the file is written with mode 0600 in a 0700 directory.
type ServerInfo struct {
	Schema    int       `json:"schema"`
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"startedAt"`
	Version   string    `json:"version,omitempty"`
	// Build identifies the executable (see ThisBuild).
	Build  string `json:"build,omitempty"`
	Secret string `json:"secret"`
}

// StateDir returns ~/.astrona/workspace (or the development profile's),
// without creating it.
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, apphome.Name(), "workspace"), nil
}

// ServerInfoPath returns the path of server.json in StateDir.
func ServerInfoPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.json"), nil
}

// ErrServerNotRunning means server.json names a process that has exited:
// the file is stale and no URL may be minted from it.
var ErrServerNotRunning = errors.New("workspace server is not running")

// Alive reports whether the process recorded in server.json is still
// running. A false result means the file is stale. PID reuse is not
// detected here; the HMAC code still only works on the server holding the
// secret.
func (i ServerInfo) Alive() bool { return processAlive(i.PID) }

// LaunchURL returns the page URL with a fresh single-use code in its
// fragment, which the browser never sends to the server or in a Referer.
// It returns ErrServerNotRunning when the recorded process has exited.
func (i ServerInfo) LaunchURL(ttl time.Duration) (string, error) {
	if !i.Alive() {
		return "", ErrServerNotRunning
	}
	secret, err := b64.DecodeString(i.Secret)
	if err != nil {
		return "", errors.New("server.json has an invalid secret")
	}
	if i.Port <= 0 || i.Port > 65535 {
		return "", fmt.Errorf("server.json has an invalid port %d", i.Port)
	}
	code, err := MintCode(secret, time.Now(), ttl)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:%d/#code=%s", loopbackAddr, i.Port, url.QueryEscape(code)), nil
}

// WriteServerInfo atomically writes info to path with mode 0600. It creates
// the directory with mode 0700, tightens an existing one to 0700, and
// refuses a directory that is a symlink or (on Unix) owned by another user.
func WriteServerInfo(path string, info ServerInfo) error {
	data, err := json.MarshalIndent(info, "", "  ") //nolint:gosec // G117: server.json exists to hand the secret to the CLI; it is written 0600
	if err != nil {
		return fmt.Errorf("failed to marshal server.json: %w", err)
	}
	return writePrivateFile(path, data)
}

// writePrivateFile atomically writes data to path with mode 0600, creating
// and checking its directory as WriteServerInfo describes.
func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	if err := privateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*") // mode 0600
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return fmt.Errorf("failed to restrict %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to replace %s: %w", path, err)
	}
	return nil
}

// privateDir makes sure dir is a real directory of the current user with
// mode 0700, tightening the mode when needed.
func privateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory (a symlink is refused)", dir)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("failed to restrict %s: %w", dir, err)
		}
		if fi, err = os.Lstat(dir); err != nil {
			return err
		}
	}
	return checkPrivate(dir, fi)
}

// ReadServerInfo reads server.json. It opens the file without following a
// symlink (where the OS supports that), then checks the opened file itself:
// it must be a regular file and, on Unix, owned by the user and not
// accessible by group or others, since it holds the launch-code secret. The
// directory must pass the same checks. A file whose process has exited is
// returned as is; check Alive before using it.
func ReadServerInfo(path string) (ServerInfo, error) {
	var info ServerInfo
	data, err := readPrivateFile(path, 64<<10)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if info.Schema != serverInfoSchema {
		return info, fmt.Errorf("%s has unsupported schema %d", path, info.Schema)
	}
	return info, nil
}

// readPrivateFile reads at most limit bytes of path with the checks
// ReadServerInfo describes: a private directory, no symlink, a regular file
// that only the user can access.
func readPrivateFile(path string, limit int64) ([]byte, error) {
	if dfi, err := os.Lstat(filepath.Dir(path)); err != nil {
		return nil, err
	} else if !dfi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory (a symlink is refused)", filepath.Dir(path))
	} else if err := checkPrivate(filepath.Dir(path), dfi); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if err := checkPrivate(path, fi); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return data, nil
}

// RemoveServerInfo deletes server.json if it still describes the process
// pid, so a server that exits never removes its successor's file.
func RemoveServerInfo(path string, pid int) error {
	info, err := ReadServerInfo(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.PID != pid {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
