package main

import (
	"archive/tar"
	"astrona/internal/apphome"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/resources"

	"github.com/mattn/go-isatty"
)

// shQuote quotes s for a POSIX shell — everything sent to the VM goes
// through the remote user's shell, so every piece of it is quoted.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// vmResourceCommand runs the workspace helper against the student's editable
// files. Values and arguments are quoted separately, never re-evaluated.
func vmResourceCommand(r resources.Resource, args []string) (string, error) {
	if r.How == resources.HowFile {
		return "", fmt.Errorf("%s is a file, not something to run — use astrona res show or copy", r.Name)
	}
	argv := []string{"res", "run", r.File, "--"}
	argv = append(argv, args...)
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = shQuote(arg)
	}
	return `"$HOME/.astrona-resources/bin/astrona" ` + strings.Join(quoted, " "), nil
}

// vmTarget is the qemu VM a resource runs in: --vm, else the resource's
// vm:, else the lab's only VM. It returns the name its state is kept under
// (the lab name, or "<lab>-<vm>" in a multi-VM lab).
func vmTarget(lab string, r resources.Resource, vmFlag string) (string, error) {
	vm := vmFlag
	if vm == "" {
		vm = r.VM
	}
	if vm != "" {
		if err := config.ValidateName(vm); err != nil {
			return "", fmt.Errorf("--vm: %w", err)
		}
		if qemuStateExists(lab + "-" + vm) {
			return lab + "-" + vm, nil
		}
		return "", fmt.Errorf("lab %s has no running VM %q", strings.TrimPrefix(lab, apphome.ClusterPrefix()), vm)
	}
	if qemuStateExists(lab) {
		return lab, nil
	}
	var vms []string
	if rows, _, err := collectQEMURows(); err == nil {
		for _, row := range rows {
			if v, ok := strings.CutPrefix(row.name, lab+"-"); ok {
				vms = append(vms, v)
			}
		}
	}
	if len(vms) == 0 {
		return "", fmt.Errorf("lab %s isn't running — start it with `astrona run`", strings.TrimPrefix(lab, apphome.ClusterPrefix()))
	}
	return "", fmt.Errorf("lab %s has several VMs (%s) — pick one: --vm <name>", strings.TrimPrefix(lab, apphome.ClusterPrefix()), strings.Join(vms, ", "))
}

// runResourceInVM uses the same persistent workspace as interactive VM tabs.
func runResourceInVM(lab, _ string, r resources.Resource, args []string, vmFlag string, headless bool) error {
	target, err := vmTarget(lab, r, vmFlag)
	if err != nil {
		return err
	}
	handle, err := hypervisor.LoadQEMUHandle(target)
	if err != nil {
		return err
	}
	if err := prepareVMResourceWorkspace(context.Background(), lab, handle); err != nil {
		return err
	}
	remote, err := vmResourceCommand(r, args)
	if err != nil {
		return err
	}
	remote = "env ASTRONA_LAB=" + shQuote(lab) + " ASTRONA_RESOURCE_TARGET=" + shQuote(target) + " " + remote
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	base := vmResourceSSHArgs(handle)
	if headless {
		base = append([]string{"-T"}, base...)
	} else if isatty.IsTerminal(os.Stdin.Fd()) {
		base = append([]string{"-t"}, base...)
	}
	return runAttached(exec.Command(sshPath, append(base, remote)...))
}

// writeResourceTar writes src (a file or a folder) as a tar whose entries
// are relative to src's folder — what `tar -x` unpacks in the VM. Only
// regular files and folders exist in a resource copy.
func writeResourceTar(w io.Writer, src string) error {
	tw := tar.NewWriter(w)
	root := filepath.Dir(src)
	// Files are opened through an os.Root of the copy's folder: a link
	// swapped in while walking can't lead the read outside it.
	rt, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer rt.Close()
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s isn't a regular file", rel)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := rt.Open(rel)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, io.LimitReader(f, hdr.Size))
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
