//go:build windows

package workspace

import "errors"

// spawnServer is not supported on native Windows: labs run inside WSL,
// where the Linux build of astrona serves the workspace.
func spawnServer() (int, error) {
	return 0, errors.New("the browser workspace is not supported on native Windows; run astrona inside WSL")
}
