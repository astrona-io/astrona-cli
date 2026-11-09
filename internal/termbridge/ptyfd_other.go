//go:build !unix

package termbridge

import "os"

// pollable and setsize are never reached where there is no PTY (Start
// returns ErrUnsupported first); they exist so the package builds there.
func pollable(f *os.File) (*os.File, error) { return f, nil }

func setsize(*os.File, uint16, uint16) error { return ErrUnsupported }
