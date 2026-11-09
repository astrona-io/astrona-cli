//go:build !unix

package termbridge

import "os"

// pollable is never reached where there is no PTY (Start returns
// ErrUnsupported first); it exists so the package builds there.
func pollable(f *os.File) (*os.File, error) { return f, nil }
