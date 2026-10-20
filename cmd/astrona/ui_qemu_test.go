package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/labstate"

	tea "charm.land/bubbletea/v2"
)

func saveUIVM(t *testing.T, lab, vm string, pid int) *config.QEMUHandle {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), apphome.Name(), "qemu", vm)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	h := &config.QEMUHandle{ClusterName: vm, LabName: lab, PID: pid, SSHHost: "127.0.0.1", SSHPort: 2222, SSHUser: "student", SSHKeyPath: filepath.Join(dir, "student-key"), AdminKeyPath: filepath.Join(dir, "admin-key"), KnownHosts: filepath.Join(dir, "known_hosts")}
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "handle.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestUIQEMUDiscoveryAndPausedActivityWithoutEngine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if groups, err := readUIQEMUVMs(); err != nil || len(groups) != 0 {
		t.Fatalf("empty discovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), apphome.Name())); !os.IsNotExist(err) {
		t.Fatal("discovery created state")
	}
	st := &labstate.State{Source: &labstate.Source{Config: "./lab"}, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := labstate.Save("astro-vms", st); err != nil {
		t.Fatal(err)
	}
	saveUIVM(t, "astro-vms", "astro-vms-client", os.Getpid())
	saveUIVM(t, "astro-vms", "astro-vms-server", 0)
	saveUIVM(t, "astro-other", "astro-other", os.Getpid())
	data := readUIHome(t.Context())
	if data.warning != "" || len(data.runs) != 1 || !data.runs[0].openable || data.runs[0].status != "Degraded (1/2 VMs)" || data.runs[0].activity != "Paused" {
		t.Fatalf("QEMU home: %+v", data)
	}
	names, err := workspaceLabNames()
	if err != nil || !slices.Equal(names, []string{"astro-vms"}) {
		t.Fatalf("names=%v err=%v", names, err)
	}
	if err := labstate.SavePresence("astro-vms", st.StartedAt, "viewer", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := readUIHome(t.Context()).runs[0].activity; got != "Active" {
		t.Fatal(got)
	}
	if err := labstate.SavePresence("astro-vms", st.StartedAt, "viewer", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := readUIHome(t.Context()).runs[0].activity; got != "Paused" {
		t.Fatal(got)
	}
}

func TestUIQEMUTargetsAndStudentTerminal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{Name: "client"}, {Name: "server"}}}}
	targets, err := workspaceQEMUTargets(cfg, "astro-vms")
	if err != nil || len(targets) != 2 || targets[1].name != "astro-vms-server" || targets[1].alias != "server" {
		t.Fatalf("targets: %+v %v", targets, err)
	}
	h := saveUIVM(t, "astro-vms", "astro-vms-server", os.Getpid())
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\ncase \"$*\" in *astrona-resources/id*) exit 0 ;; esac\nexec /bin/sh -i\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	cmd, err := workspaceQEMUCommand(t.Context(), "astro-vms", h.ClusterName)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"-tt", "-l student", h.SSHKeyPath, h.KnownHosts, "-- 127.0.0.1", "astrona-cli >", "-F /dev/null", "EscapeChar=none", "PermitLocalCommand=no", "ClearAllForwardings=yes", "ForwardAgent=no", "ForwardX11=no", "$HOME/.astrona/runtime/astro-vms"} {
		if !strings.Contains(args, want) {
			t.Fatalf("SSH arguments missing %q: %v", want, cmd.Args)
		}
	}
	if strings.Contains(args, h.AdminKeyPath) {
		t.Fatal("student terminal uses administrative identity")
	}
	manager := newUITerminals(t.Context(), func(context.Context, string) (*exec.Cmd, error) { return cmd, nil })
	t.Cleanup(manager.close)
	if result := manager.start("server", 80, 24)().(uiTerminalStarted); result.err != nil {
		t.Fatal(result.err)
	}
	tab := manager.snapshot()[0]
	_, _ = io.WriteString(tab.terminal, "printf 'ssh-%s\\n' works\n")
	waitUITab(t, tab, "ssh-works")
	if _, err := workspaceQEMUCommand(t.Context(), "astro-other", h.ClusterName); err == nil {
		t.Fatal("accepted another lab's VM")
	}
	saveUIVM(t, "astro-vms", "astro-vms-server", 0)
	if _, err := workspaceQEMUCommand(t.Context(), "astro-vms", h.ClusterName); err == nil {
		t.Fatal("connected to a stopped VM")
	}
	cfg.Runtime.QEMU = []config.QEMUVM{{}}
	targets, err = workspaceQEMUTargets(cfg, "astro-one")
	if err != nil || len(targets) != 1 || targets[0].name != "astro-one" {
		t.Fatalf("single VM: %+v %v", targets, err)
	}
}

func TestUIQEMUNewTabChoosesVM(t *testing.T) {
	manager := testUITerminals(t)
	m := &uiTaskModel{workspace: workspaceModel{width: 120, height: 30, snapshot: workspaceSnapshot{clusters: []workspaceCluster{{name: "client", alias: "client", runtime: "qemu"}, {name: "server", alias: "server", runtime: "qemu"}}}}, terminals: manager, sized: true}
	m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.workspace.selected != 1 || !strings.Contains(m.render(), "VM: server") {
		t.Fatal("VM chooser did not move")
	}
	_, start := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if start == nil {
		t.Fatal("no new terminal")
	}
	if result := start().(uiTerminalStarted); result.err != nil {
		t.Fatal(result.err)
	}
	if tab := manager.snapshot()[0]; tab.alias != "server" || !strings.HasPrefix(tab.label, "server") {
		t.Fatal("wrong terminal target")
	}
}

func TestUIQEMUResourcesVerifyProcessIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	expected := map[int]string{10: "astro-vms-client", 20: "astro-vms-server", 30: "astro-other"}
	samples := parseUIQEMUUsage("10 12.5 2048 /usr/bin/qemu-system-aarch64 -name astrona-astro-vms-client\n20 7.5 1024 qemu-system-x86_64 -name astrona-astro-vms-server\n30 999 999 unrelated-process -name astrona-astro-other\n", expected)
	if len(samples) != 2 {
		t.Fatal(samples)
	}
	vms := []*config.QEMUHandle{{PID: 10}, {PID: 20}}
	usage := uiQEMURunUsage("astro-vms", time.Now().UTC().Format(time.RFC3339), vms, samples)
	if usage.CPU != 20 || usage.Memory != 3*1024*1024 || usage.Samples != 1 {
		t.Fatalf("bad QEMU usage: %+v", usage)
	}
	delete(samples, 20)
	if uiQEMURunUsage("astro-vms", time.Now().UTC().Format(time.RFC3339), vms, samples).Samples != 0 {
		t.Fatal("recorded incomplete VM totals")
	}
}

func TestUIQEMURestartWithoutContainerEngine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	lab := "ATS000/section-010/module-01/lab-01"
	restartState(t, "astro-vms", lab)
	saveUIVM(t, "astro-vms", "astro-vms-server", 0)
	cmd, flags := uiRestartCommand(t, lab)
	restart, err := findUIRunRestart(cmd, flags, func(context.Context) (map[string][]kindContainer, error) {
		t.Fatal("QEMU restart queried a container engine")
		return nil, nil
	})
	if err != nil || restart == nil || restart.name != "astro-vms" {
		t.Fatalf("restart=%+v err=%v", restart, err)
	}
	flags.uiRestart = restart
	if err := approveUIRestart(cmd, flags, restart.name, nil); err != nil {
		t.Fatal(err)
	}
	if !flags.localOnly {
		t.Fatal("QEMU restart can upload results")
	}
}
