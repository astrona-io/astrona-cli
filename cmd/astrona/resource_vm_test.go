package main

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
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
			`cd 'astrona-resources/setup' && 'bash' 'setup.sh' '--x' '$(id)'`},
		{resources.Resource{Name: "check", File: "tools/check", How: resources.HowExec}, nil,
			`cd 'astrona-resources/check' && './check'`},
		{resources.Resource{Name: "load-test", File: "load-test", Dir: true, How: resources.HowCommand, Run: "go run ."}, []string{"50"},
			`cd 'astrona-resources/load-test/load-test' && 'go' 'run' '.' '50'`},
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
	dir := snapshotLab(t, "astro-vmlab", map[string]string{"setup.sh": "echo hi", "tool/main.go": "package main"},
		config.LabResource{File: "tool", Run: "go run ."})
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
	calls := filepath.Join(bin, "calls")
	upload := filepath.Join(bin, "upload.tar")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + calls + "\ncase \"$*\" in *'tar -xf -'*) cat > " + upload + " ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	tool, _ := resources.Find(list, "tool")
	if err := runResourceInVM("astro-vmlab", dir, tool, []string{"--rps", "50"}, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("ssh calls:\n%s", b)
	}
	for _, want := range []string{"-p 2222", "-i /k/student", "UserKnownHostsFile=/k/known_hosts", "student@127.0.0.1", "BatchMode=yes",
		"rm -rf 'astrona-resources/tool' && mkdir -p 'astrona-resources/tool' && tar -xf - -C 'astrona-resources/tool'"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("upload call lacks %q:\n%s", want, lines[0])
		}
	}
	if !strings.HasSuffix(lines[1], `cd 'astrona-resources/tool/tool' && 'go' 'run' '.' '--rps' '50'`) || !strings.Contains(lines[1], "student@127.0.0.1") {
		t.Errorf("run call:\n%s", lines[1])
	}

	f, err := os.Open(upload)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var names []string
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	if strings.Join(names, ",") != "tool/,tool/main.go" {
		t.Errorf("tar entries = %v", names)
	}

	// Several VMs and no --vm / vm: it asks which.
	if _, err := vmTarget("astro-none", tool, ""); err == nil || !strings.Contains(err.Error(), "isn't running") {
		t.Errorf("no VM = %v", err)
	}
	if _, err := vmTarget("astro-vmlab", tool, "nope"); err == nil || !strings.Contains(err.Error(), `no running VM "nope"`) {
		t.Errorf("unknown --vm = %v", err)
	}
}
