package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/hypervisor"
)

// Unlike the runtime's creating path helpers, UI discovery only reads state.
func readUIQEMUHandle(name string) (*config.QEMUHandle, error) {
	if err := config.ValidateName(name); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(home, apphome.Name(), "qemu", name, "handle.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var h config.QEMUHandle
	if err := json.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&h); err != nil {
		return nil, fmt.Errorf("read VM %s: %w", name, err)
	}
	if h.ClusterName != name {
		return nil, fmt.Errorf("VM state does not match %s", name)
	}
	if h.LabName == "" {
		h.LabName = name
	}
	if err := config.ValidateName(h.LabName); err != nil || (h.LabName != name && !strings.HasPrefix(name, h.LabName+"-")) {
		return nil, fmt.Errorf("VM %s has an invalid lab owner", name)
	}
	return &h, nil
}

func readUIQEMUVMs() (map[string][]*config.QEMUHandle, error) {
	groups := map[string][]*config.QEMUHandle{}
	home, err := os.UserHomeDir()
	if err != nil {
		return groups, err
	}
	entries, err := os.ReadDir(filepath.Join(home, apphome.Name(), "qemu"))
	if os.IsNotExist(err) {
		return groups, nil
	}
	if err != nil {
		return groups, err
	}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), apphome.ClusterPrefix()) || strings.HasPrefix(entry.Name(), apphome.TestClusterPrefix()) {
			continue
		}
		h, err := readUIQEMUHandle(entry.Name())
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		groups[h.LabName] = append(groups[h.LabName], h)
	}
	return groups, errors.Join(failures...)
}

func uiQEMUStatus(vms []*config.QEMUHandle) (string, bool) {
	running := 0
	for _, vm := range vms {
		if hypervisor.ProcessAlive(vm.PID) {
			running++
		}
	}
	if running == 0 {
		return "Stopped", false
	}
	if running != len(vms) {
		return fmt.Sprintf("Degraded (%d/%d VMs)", running, len(vms)), true
	}
	if len(vms) == 1 {
		return "Running", true
	}
	return fmt.Sprintf("Running (%d VMs)", running), true
}

func workspaceQEMUTargets(cfg *config.LabConfig, name string) ([]workspaceCluster, error) {
	if err := config.ValidateQEMUVMs(cfg.Runtime.QEMU); err != nil {
		return nil, err
	}
	if !config.IsMultiVM(cfg.Runtime.QEMU) {
		return []workspaceCluster{{name: name, runtime: "qemu", status: "Checking…"}}, nil
	}
	var targets []workspaceCluster
	for _, vm := range cfg.Runtime.QEMU {
		targets = append(targets, workspaceCluster{name: name + "-" + vm.Name, alias: vm.Name, runtime: "qemu", status: "Checking…"})
	}
	return targets, nil
}

func workspaceQEMUHealth(lab, name string) string {
	h, err := readUIQEMUHandle(name)
	if err != nil || h.LabName != lab {
		return "VM unavailable"
	}
	if !hypervisor.ProcessAlive(h.PID) {
		return "Stopped"
	}
	return "Running · SSH terminal"
}

func workspaceQEMUCommand(ctx context.Context, lab, target string) (*exec.Cmd, error) {
	h, err := readUIQEMUHandle(target)
	if err != nil {
		return nil, fmt.Errorf("read VM %s: %w", target, err)
	}
	if h.LabName != lab || !hypervisor.ProcessAlive(h.PID) {
		return nil, fmt.Errorf("VM %s is not running in lab %s", target, lab)
	}
	if h.SSHUser == "" || h.SSHHost == "" || h.SSHPort < 1 || h.SSHPort > 65535 || h.SSHKeyPath == "" || h.KnownHosts == "" {
		return nil, fmt.Errorf("VM %s has incomplete SSH connection details", target)
	}
	if err := prepareVMResourceWorkspace(ctx, lab, h); err != nil {
		return nil, err
	}
	// Use the student's identity, never the administrative bootstrap key.
	// A real remote PTY gives editors, resize and job control normal SSH behavior.
	cmd := exec.CommandContext(ctx, "ssh", append(isolatedSSHOptions(), "-tt", "-i", h.SSHKeyPath,
		"-p", strconv.Itoa(h.SSHPort), "-l", h.SSHUser,
		"-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile="+h.KnownHosts,
		"--", h.SSHHost, vmWorkspaceShell(lab, true))...)
	return cmd, nil
}

// Disable SSH features which execute commands on the host or expose its agent.
func isolatedSSHOptions() []string {
	return []string{"-F", "/dev/null", "-o", "EscapeChar=none", "-o", "PermitLocalCommand=no", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no"}
}

func vmWorkspaceShell(lab string, prompt bool) string {
	// lab has passed ValidateName before it is used in a remote command.
	dir := `"$HOME/.astrona/runtime/` + lab + `"`
	command := "mkdir -p " + dir + " && cd " + dir + ` && export PATH="$HOME/.astrona-resources/bin:$PATH" ASTRONA_RESOURCE_DIR="$HOME/resources" ASTRONA_LAB=` + shQuote(lab) + "; exec "
	if prompt {
		command += "env PS1='" + uiShellPrompt(os.Getenv("NO_COLOR") != "") + "' "
	}
	return command + "sh -i"
}
