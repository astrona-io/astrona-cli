package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"astrona/internal/config"
)

// TimeoutError is a script or check that was stopped because it ran past
// its time limit. Its whole process group was killed, so nothing it
// started is left running.
type TimeoutError struct {
	After time.Duration
}

func (e *TimeoutError) Error() string {
	return "timed out after " + FormatTimeout(e.After)
}

// FormatTimeout renders a time limit the way lab authors write it:
// "90s", "5m", "1h30m".
func FormatTimeout(d time.Duration) string { return config.FormatDuration(d) }

// WithTimeout is context.WithTimeout from context.Background, or a plain
// cancellable context when d <= 0 (no limit).
func WithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), d)
}

// killGrace is how long Run waits, after killing a command's process
// group, for its output pipes to close before giving up on them — a
// grandchild that escaped the group must not hold Run open.
const killGrace = 5 * time.Second

// Command is exec.CommandContext with the command put in a process group
// of its own: when ctx ends, the whole group is killed (the script and
// every kubectl, ssh or sleep it started), not only the direct child. Run
// it with Run or CombinedOutput, which also pass a terminal's Ctrl+C on to
// the group.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = killGrace
	return cmd
}

// Run starts cmd (made by Command) and waits for it.
func Run(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	stop := forwardSignals(cmd)
	err := cmd.Wait()
	stop()
	return err
}

// CombinedOutput is Run with stdout and stderr captured together.
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b
	err := Run(cmd)
	return b.Bytes(), err
}

// Timed turns err into a *TimeoutError when ctx's deadline (set from
// limit, see WithTimeout) is what ended the command.
func Timed(ctx context.Context, limit time.Duration, err error) error {
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &TimeoutError{After: limit}
	}
	return err
}

// IsTimeout reports whether err is (or wraps) a *TimeoutError.
func IsTimeout(err error) bool {
	var t *TimeoutError
	return errors.As(err, &t)
}

// errNoProcess is killProcessGroup on a command that never started.
var errNoProcess = fmt.Errorf("process not started")
