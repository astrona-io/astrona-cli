package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// IdleTimeout is how long the server waits with nothing to do before it
// exits.
const IdleTimeout = 15 * time.Minute

// spawnWait is how long EnsureServer waits for a new server to write
// server.json.
const spawnWait = 15 * time.Second

// RunningServer returns the live server's info, or ErrServerNotRunning.
func RunningServer() (ServerInfo, error) {
	path, err := ServerInfoPath()
	if err != nil {
		return ServerInfo{}, err
	}
	info, err := ReadServerInfo(path)
	if errors.Is(err, os.ErrNotExist) {
		return info, ErrServerNotRunning
	}
	if err != nil {
		return info, err
	}
	if !info.Alive() {
		return info, ErrServerNotRunning
	}
	return info, nil
}

// EnsureServer returns the running server, starting one in the background
// when there is none. started reports whether it started one.
func EnsureServer() (info ServerInfo, started bool, err error) {
	if info, err := RunningServer(); err == nil {
		return info, false, nil
	} else if !errors.Is(err, ErrServerNotRunning) {
		return info, false, err
	}
	pid, err := spawnServer()
	if err != nil {
		return ServerInfo{}, false, fmt.Errorf("start the workspace server: %w", err)
	}
	path, err := ServerInfoPath()
	if err != nil {
		return ServerInfo{}, false, err
	}
	deadline := time.Now().Add(spawnWait)
	for time.Now().Before(deadline) {
		if info, err := ReadServerInfo(path); err == nil && info.PID == pid {
			return info, true, nil
		}
		if !processAlive(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ServerInfo{}, false, fmt.Errorf("the workspace server did not start; see %s", filepath.Join(filepath.Dir(path), "server.log"))
}
