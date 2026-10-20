package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/resources"
)

func TestShQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       "'plain'",
		"it's":        `'it'\''s'`,
		"$(rm -rf ~)": "'$(rm -rf ~)'",
		"a b; c":      "'a b; c'",
	} {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestVMResourceCommand(t *testing.T) {
	for _, c := range []struct {
		r    resources.Resource
		args []string
		want string
	}{
		{resources.Resource{Name: "setup", File: "setup.sh", How: resources.HowBash}, []string{"--x", "$(id)"},
			`"$HOME/.astrona-resources/bin/astrona" 'res' 'run' 'setup.sh' '--' '--x' '$(id)'`},
		{resources.Resource{Name: "check", File: "tools/check", How: resources.HowExec}, nil,
			`"$HOME/.astrona-resources/bin/astrona" 'res' 'run' 'tools/check' '--'`},
		{resources.Resource{Name: "load-test", File: "load-test", Dir: true, How: resources.HowCommand, Run: "go run ."}, []string{"50"},
			`"$HOME/.astrona-resources/bin/astrona" 'res' 'run' 'load-test' '--' '50'`},
	} {
		got, err := vmResourceCommand(c.r, c.args)
		if err != nil || got != c.want {
			t.Errorf("%s = %s, %v\n want %s", c.r.Name, got, err, c.want)
		}
	}
	if _, err := vmResourceCommand(resources.Resource{Name: "notes", How: resources.HowFile}, nil); err == nil {
		t.Error("a plain file ran")
	}
}

// A qemu lab's resource is copied into the VM (tar over ssh) and run there
// as the student account — checked with a fake ssh and VM state.
func TestRunResourceInVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := snapshotLab(t, "astro-vmlab", map[string]string{"setup.sh": "echo hi", "tool/main.sh": "printf 'args:<%s>\\n' \"$@\"; cat ../sibling.txt", "sibling.txt": "shared data"},
		config.LabResource{File: "tool", Run: "sh main.sh"})
	list, _, _ := resources.Load("astro-vmlab")

	state := filepath.Join(home, ".astrona", "qemu", "astro-vmlab")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	h, _ := json.Marshal(config.QEMUHandle{ClusterName: "astro-vmlab", PID: os.Getpid(), SSHHost: "127.0.0.1", SSHPort: 2222,
		SSHUser: "student", SSHKeyPath: "/k/student", KnownHosts: "/k/known_hosts", StateDir: state})
	if err := os.WriteFile(filepath.Join(state, "handle.json"), h, 0o600); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	remoteHome := t.TempDir()
	t.Setenv("ASTRONA_TEST_VM_HOME", remoteHome)
	script := "#!/bin/sh\nfor remote do :; done\nHOME=\"$ASTRONA_TEST_VM_HOME\" exec /bin/sh -c \"$remote\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	tool, _ := resources.Find(list, "tool")
	var runErr error
	out := captureStdout(t, func() {
		runErr = runResourceInVM("astro-vmlab", dir, tool, []string{"a b", "$(touch injected)"}, "", true)
	})
	if runErr != nil || !strings.Contains(out, "args:<a b>") || !strings.Contains(out, "args:<$(touch injected)>") || !strings.Contains(out, "shared data") {
		t.Fatalf("VM resource: %v\n%s", runErr, out)
	}
	file := filepath.Join(remoteHome, "resources", "sibling.txt")
	if err := os.WriteFile(file, []byte("student edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { runErr = runResourceInVM("astro-vmlab", dir, tool, nil, "", true) })
	if runErr != nil || !strings.Contains(out, "student edit") {
		t.Fatalf("reopening replaced VM edits: %v %s", runErr, out)
	}
	if _, err := os.Stat(filepath.Join(remoteHome, "resources/tool/injected")); !os.IsNotExist(err) {
		t.Fatal("evaluated resource arguments")
	}

	// Several VMs and no --vm / vm: it asks which.
	if _, err := vmTarget("astro-none", tool, ""); err == nil || !strings.Contains(err.Error(), "isn't running") {
		t.Errorf("no VM = %v", err)
	}
	if _, err := vmTarget("astro-vmlab", tool, "nope"); err == nil || !strings.Contains(err.Error(), `no running VM "nope"`) {
		t.Errorf("unknown --vm = %v", err)
	}
}
