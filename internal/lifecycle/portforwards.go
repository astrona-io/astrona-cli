package lifecycle

import (
	"astrona/internal/config"
	"astrona/internal/portforward"
	"astrona/internal/ui"
	"time"
)

// PortForwardReadyTimeout is how long `run` / `port-forward start` wait for
// every forward to report Ready before printing what they have.
const PortForwardReadyTimeout = 30 * time.Second

func CountNotReady(fs []portforward.Forward) int {
	n := 0
	for _, f := range fs {
		if f.Effective() != portforward.StateReady {
			n++
		}
	}
	return n
}

// StartPortForwards starts cfg's forwards for clusterName and waits for
// them. Shared by `run` and `port-forward start`. Never fails the caller
// for a forward that's merely slow — the supervisor keeps retrying.
func StartPortForwards(clusterName string, forwards []config.PortForward, rep *ui.Reporter) ([]portforward.Forward, error) {
	startErr := portforward.Start(clusterName, forwards, rep)
	if startErr != nil {
		rep.Info("port forward start errors: %s", startErr)
	}

	t := rep.Step("Wait for port forwards to become ready")
	fs, err := portforward.WaitReady(clusterName, PortForwardReadyTimeout)
	if err != nil {
		return nil, t.Fail(err)
	}
	if n := CountNotReady(fs); n > 0 {
		t.Skip("%d of %d not ready after %s — still retrying in the background", n, len(fs), PortForwardReadyTimeout)
	} else {
		t.Done()
	}
	return fs, startErr
}

// --- port-forward start ----------------------------------------------------
