package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/resources"
	"astrona/internal/ui"
)

func TestOtherQEMULabs(t *testing.T) {
	vm := func(name, lab string) labRow { return labRow{name: name, lab: lab} }
	for _, c := range []struct {
		name string
		rows []labRow
		want []string
	}{
		{"multi-VM lab is one lab",
			[]labRow{vm("astro-net-vm1", "astro-net"), vm("astro-net-vm2", "astro-net"), vm("astro-db", "astro-db")},
			[]string{"astro-net", "astro-db"}},
		{"own VMs are skipped",
			[]labRow{vm("astro-k8s-basics-vm1", "astro-k8s-basics"), vm("astro-k8s-basics", "astro-k8s-basics")},
			nil},
		{"a longer lab name isn't this lab's VM",
			[]labRow{vm("astro-k8s-basics-01", "astro-k8s-basics-01"), vm("astro-k8s-basics-01-vm1", "astro-k8s-basics-01")},
			[]string{"astro-k8s-basics-01"}},
		{"test copies are skipped",
			[]labRow{vm("astro-test-x-vm1", "astro-test-x")},
			nil},
		{"old handle without a lab: its own name, unless it may be this lab's",
			[]labRow{vm("astro-db", ""), vm("astro-k8s-basics-vm1", "")},
			[]string{"astro-db"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := otherQEMULabs("astro-k8s-basics", c.rows); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// Destroying a multi-VM qemu lab by its name destroys every VM and forgets
// the lab (its state and resources copy), not just one VM.
func TestDestroyByNameMultiVMLab(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHandle := func(clusterName, lab string) {
		dir := filepath.Join(home, ".astrona", "qemu", clusterName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(config.QEMUHandle{ClusterName: clusterName, LabName: lab})
		if err := os.WriteFile(filepath.Join(dir, "handle.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeHandle("astro-zz-net-vm1", "astro-zz-net")
	writeHandle("astro-zz-net-vm2", "astro-zz-net")
	writeHandle("astro-zz-net-01", "astro-zz-net-01") // another lab, kept
	if err := labstate.Save("astro-zz-net", &labstate.State{Source: &labstate.Source{Config: "x"}}); err != nil {
		t.Fatal(err)
	}
	copyDir, _ := resources.Dir("astro-zz-net")
	if err := os.MkdirAll(copyDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := destroyByName("astro-zz-net", ui.Discard()); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		filepath.Join(home, ".astrona", "qemu", "astro-zz-net-vm1"),
		filepath.Join(home, ".astrona", "qemu", "astro-zz-net-vm2"),
		copyDir,
	} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s survived", gone)
		}
	}
	if st, _ := labstate.Load("astro-zz-net"); st != nil {
		t.Errorf("lab state survived: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(home, ".astrona", "qemu", "astro-zz-net-01")); err != nil {
		t.Errorf("an unrelated lab was destroyed: %v", err)
	}
}

func TestChooseRun(t *testing.T) {
	others := []string{"astro-net-01", "astro-db-02"}
	for _, c := range []struct {
		name               string
		tty, yes, exists   bool
		answers            string // one line per prompt
		others             []string
		want               runChoice
		wantErr, wantAsked string
	}{
		{name: "nothing running", tty: true, want: runChoice{}},
		{name: "running, yes to start over", tty: true, exists: true, answers: "y\n",
			want: runChoice{startOver: true}, wantAsked: "is already running. Destroy it and start over?"},
		{name: "running, no keeps it", tty: true, exists: true, answers: "\n",
			want: runChoice{keep: true}},
		{name: "running, --yes doesn't ask", exists: true, yes: true,
			want: runChoice{startOver: true}},
		{name: "running, no terminal, no --yes", exists: true,
			wantErr: "pass --yes to destroy it and start over"},
		{name: "others, yes stops them", tty: true, answers: "y\n", others: others,
			want: runChoice{stopOthers: others}, wantAsked: "Also running: net-01, db-02. Destroy them first?"},
		{name: "others, no keeps them", tty: true, answers: "n\n", others: others,
			want: runChoice{}},
		{name: "both: restart, and stop the other", tty: true, exists: true, answers: "y\ny\n", others: others[:1],
			want: runChoice{startOver: true, stopOthers: others[:1]}, wantAsked: "Destroy it first?"},
		{name: "--yes never stops others", tty: true, exists: true, yes: true, others: others,
			want: runChoice{startOver: true}},
		{name: "no terminal never asks about others", others: others,
			want: runChoice{}},
		{name: "kept lab: others not asked", tty: true, exists: true, answers: "n\n", others: others,
			want: runChoice{keep: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := chooseRun(strings.NewReader(c.answers), &out, c.tty, c.yes, c.exists, "lab-01", c.others)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("choice = %+v, want %+v", got, c.want)
			}
			if c.wantAsked != "" && !strings.Contains(out.String(), c.wantAsked) {
				t.Errorf("prompt %q missing in:\n%s", c.wantAsked, out.String())
			}
			if !c.tty && out.Len() > 0 {
				t.Errorf("asked without a terminal:\n%s", out.String())
			}
		})
	}
}
