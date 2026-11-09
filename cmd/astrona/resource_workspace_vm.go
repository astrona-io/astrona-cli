package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/resources"
	"astrona/internal/sshopts"
)

func vmResourceSSHArgs(h *config.QEMUHandle) []string {
	return append(sshopts.Unattended(5), "-i", h.SSHKeyPath, "-p", strconv.Itoa(h.SSHPort),
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile="+h.KnownHosts,
		"--", h.SSHUser+"@"+h.SSHHost)
}

// The student's SSH identity installs only into that student's home. No sudo,
// host execution bridge or automatic resource execution is involved.
func prepareVMResourceWorkspace(ctx context.Context, lab string, h *config.QEMUHandle) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	release, err := labstate.LockMutation(lab)
	if err != nil {
		return err
	}
	defer release()
	id, err := resources.WorkspaceID(lab)
	if err != nil {
		return err
	}
	args := vmResourceSSHArgs(h)
	probe := exec.CommandContext(ctx, "ssh", append(args, `test "$(cat "$HOME/.astrona-resources/id" 2>/dev/null)" = `+labops.ShellQuote(id))...)
	if err := probe.Run(); err == nil {
		return nil
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("check VM resources: %w", err)
		}
	}
	bundle, _, cleanup, err := resources.WorkspaceBundle(lab)
	if err != nil {
		return err
	}
	defer cleanup()
	return uploadVMResourceBundle(ctx, args, bundle, id)
}

// Keep archive ownership and SSH transfer together. The caller owns the lab
// mutation lock and receipt check; this helper always removes its temporary tar.
func uploadVMResourceBundle(ctx context.Context, args []string, bundle, id string) error {
	command := vmResourceInstallCommand(id)
	upload := exec.CommandContext(ctx, "ssh", append(args, command)...)
	archive, err := os.CreateTemp("", "astrona-vm-resources-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if err := writeResourceTar(archive, bundle); err != nil {
		return err
	}
	if _, err := archive.Seek(0, 0); err != nil {
		return err
	}
	upload.Stdin = archive
	var output cappedOutput
	upload.Stdout, upload.Stderr = &output, &output
	if err := upload.Run(); err != nil {
		return fmt.Errorf("prepare VM resources: %w: %s", err, strings.TrimSpace(output.buffer.String()))
	}
	return nil
}

func vmResourceInstallCommand(id string) string {
	return `set -eu; umask 077
base="$HOME/.astrona-resources"
test ! -L "$base" && test ! -L "$HOME/resources"
mkdir -p "$base"
if [ -e "$HOME/resources" ] && [ ! -f "$base/id" ]; then
  echo 'Resources already exist without an Astrona receipt; move ~/resources aside first.' >&2; exit 1
fi
stage=$(mktemp -d "$base/stage.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
tar -xf - -C "$stage"
test -d "$stage/workspace/resources" && test -f "$stage/workspace/bin/astrona"
rm -rf "$HOME/resources" "$base/bin"
mv "$stage/workspace/resources" "$HOME/resources"
mv "$stage/workspace/bin" "$base/bin"
printf '%s\n' ` + labops.ShellQuote(id) + ` > "$base/id"
`
}

// cappedOutput keeps a misbehaving VM from filling host memory through the
// upload's output: writes past 1 MiB fail.
type cappedOutput struct{ buffer bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 1024*1024 {
		return 0, io.ErrShortBuffer
	}
	return b.buffer.Write(p)
}
