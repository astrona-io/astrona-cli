package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/resources"
)

func vmResourceSSHArgs(h *config.QEMUHandle) []string {
	return append(isolatedSSHOptions(), "-i", h.SSHKeyPath, "-p", strconv.Itoa(h.SSHPort),
		"-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
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
	probe := exec.CommandContext(ctx, "ssh", append(args, `test "$(cat "$HOME/.astrona-resources/id" 2>/dev/null)" = `+shQuote(id))...)
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
	var output workspaceOutput
	upload.Stdout, upload.Stderr = &output, &output
	err = upload.Run()
	if err != nil {
		return fmt.Errorf("prepare VM resources: %w: %s", err, strings.TrimSpace(string(output.Bytes())))
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
printf '%s\n' ` + shQuote(id) + ` > "$base/id"
`
}
