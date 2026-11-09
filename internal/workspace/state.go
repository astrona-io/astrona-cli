package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
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
	Secret    string    `json:"secret"`
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

// LaunchURL returns the page URL with a fresh single-use code in its
// fragment, which the browser never sends to the server or in a Referer.
func (i ServerInfo) LaunchURL(ttl time.Duration) (string, error) {
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

// WriteServerInfo atomically writes info to path with mode 0600, creating
// the directory with mode 0700.
func WriteServerInfo(path string, info ServerInfo) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(info, "", "  ") //nolint:gosec // G117: server.json exists to hand the secret to the CLI; it is written 0600
	if err != nil {
		return fmt.Errorf("failed to marshal server.json: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".server.json.*") // mode 0600
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

// ReadServerInfo reads server.json. On Unix it refuses a file that other
// users can read or write, since it holds the launch-code secret.
func ReadServerInfo(path string) (ServerInfo, error) {
	var info ServerInfo
	fi, err := os.Lstat(path)
	if err != nil {
		return info, err
	}
	if !fi.Mode().IsRegular() {
		return info, fmt.Errorf("%s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return info, fmt.Errorf("%s is accessible by other users (mode %v); remove it and restart the workspace", path, fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
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
