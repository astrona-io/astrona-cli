package main

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/resources"

	"github.com/mattn/go-isatty"
)

// vmResourceDir is where a resource is put inside a qemu lab's VM, under
// the student's home: ~/astrona-resources/<name>.
const vmResourceDir = "astrona-resources"

// shQuote quotes s for a POSIX shell — everything sent to the VM goes
// through the remote user's shell, so every piece of it is quoted.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// vmResourceCommand is the shell command that runs r inside the VM, from
// the folder it was copied to (vmResourceDir/<name>).
func vmResourceCommand(r resources.Resource, args []string) (string, error) {
	remote := vmResourceDir + "/" + r.Name
	base := path.Base(r.File)
	var argv []string
	switch r.How {
	case resources.HowBash:
		argv = []string{"bash", base}
	case resources.HowExec:
		argv = []string{"./" + base}
	case resources.HowCommand:
		argv = strings.Fields(r.Run)
		if len(argv) == 0 {
			return "", fmt.Errorf("resource %s has an empty run command", r.Name)
		}
	default:
		return "", fmt.Errorf("%s is a file, not something to run — `astrona res show %s` prints it, `astrona res copy %s` copies it here", r.Name, r.Name, r.Name)
	}
	quoted := make([]string, 0, len(argv)+len(args))
	for _, a := range append(argv, args...) {
		quoted = append(quoted, shQuote(a))
	}
	in := remote // a file is unpacked into it…
	if r.Dir {
		in = remote + "/" + base // …a folder as itself, inside it
	}
	return "cd " + shQuote(in) + " && " + strings.Join(quoted, " "), nil
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
		return "", fmt.Errorf("lab %s has no running VM %q", strings.TrimPrefix(lab, "astro-"), vm)
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
		return "", fmt.Errorf("lab %s isn't running — start it with `astrona run`", strings.TrimPrefix(lab, "astro-"))
	}
	return "", fmt.Errorf("lab %s has several VMs (%s) — pick one: --vm <name>", strings.TrimPrefix(lab, "astro-"), strings.Join(vms, ", "))
}

// runResourceInVM copies r into the VM as the student account (the one
// `astrona ssh` uses) and runs it there.
func runResourceInVM(lab, dir string, r resources.Resource, args []string, vmFlag string) error {
	remoteCmd, err := vmResourceCommand(r, args)
	if err != nil {
		return err
	}
	target, err := vmTarget(lab, r, vmFlag)
	if err != nil {
		return err
	}
	handle, err := hypervisor.LoadQEMUHandle(target)
	if err != nil {
		return err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}
	base := []string{
		"-p", strconv.Itoa(handle.SSHPort),
		"-i", handle.SSHKeyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + handle.KnownHosts,
		handle.SSHUser + "@" + handle.SSHHost,
	}

	// 1. Copy: a tar of the resource over stdin, unpacked into a fresh
	// ~/astrona-resources/<name>.
	src, err := r.Path(dir)
	if err != nil {
		return err
	}
	remote := shQuote(vmResourceDir + "/" + r.Name)
	upload := exec.Command(sshPath, append([]string{"-o", "BatchMode=yes"}, append(base, "rm -rf "+remote+" && mkdir -p "+remote+" && tar -xf - -C "+remote)...)...)
	pr, pw := io.Pipe()
	upload.Stdin = pr
	var uploadErr strings.Builder
	upload.Stderr = &uploadErr
	if err := upload.Start(); err != nil {
		return fmt.Errorf("copy %s into the VM: %w", r.Name, err)
	}
	werr := writeResourceTar(pw, src)
	pw.CloseWithError(werr)
	if err := upload.Wait(); err != nil || werr != nil {
		if werr != nil {
			err = werr
		}
		return fmt.Errorf("copy %s into the VM: %w %s", r.Name, err, strings.TrimSpace(uploadErr.String()))
	}

	// 2. Run it there; a terminal is passed through when there is one.
	runArgs := base
	if isatty.IsTerminal(os.Stdin.Fd()) {
		runArgs = append([]string{"-t"}, base...)
	}
	c := exec.Command(sshPath, append(runArgs, remoteCmd)...)
	fmt.Fprintf(os.Stderr, "→ %s  (lab %s, VM %s, as %s, in ~/%s/%s)\n", strings.Join(append([]string{r.Command()}, args...), " "),
		strings.TrimPrefix(lab, "astro-"), strings.TrimPrefix(target, "astro-"), handle.SSHUser, vmResourceDir, r.Name)
	return runAttached(c)
}

// writeResourceTar writes src (a file or a folder) as a tar whose entries
// are relative to src's folder — what `tar -x` unpacks in the VM. Only
// regular files and folders exist in a resource copy.
func writeResourceTar(w io.Writer, src string) error {
	tw := tar.NewWriter(w)
	root := filepath.Dir(src)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
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
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
