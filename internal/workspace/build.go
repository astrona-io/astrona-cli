package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// A server is reused across astrona commands, so a newer binary can find a
// server started by an older one, with an older page and API. Development
// builds all report version "developer", so the server also records which
// executable it is: a digest of the binary.

var thisBuild = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
})

// ThisBuild identifies the running executable ("" when it cannot be read).
func ThisBuild() string { return thisBuild() }

// SameBuild reports whether the server runs this executable. A server that
// does not say (an older build) is a different one.
func (i ServerInfo) SameBuild() bool {
	return ThisBuild() != "" && i.Build == ThisBuild()
}

// ErrOtherBuildInUse is returned by EnsureCurrentServer when the running
// server is another build and has terminals or pages open.
var ErrOtherBuildInUse = errors.New("the workspace server running is from another astrona build and is in use")

// EnsureCurrentServer is EnsureServer for features the page needs from this
// build. A server from another build is stopped and replaced when nothing
// uses it; one with open terminals or pages is left alone (ErrOtherBuildInUse).
func EnsureCurrentServer(ctx context.Context) (ServerInfo, bool, error) {
	info, err := RunningServer()
	if err == nil && !info.SameBuild() {
		st, err := info.Status(ctx)
		if err != nil {
			return info, false, fmt.Errorf("check the running workspace server: %w", err)
		}
		if len(st.Terminals) > 0 || st.Clients > 0 {
			return info, false, fmt.Errorf("%w (%d terminals, %d pages)", ErrOtherBuildInUse, len(st.Terminals), st.Clients)
		}
		if err := info.Stop(ctx); err != nil {
			return info, false, fmt.Errorf("stop the older workspace server: %w", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for processAlive(info.PID) {
			if time.Now().After(deadline) {
				return info, false, errors.New("the older workspace server did not stop")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return EnsureServer()
}
