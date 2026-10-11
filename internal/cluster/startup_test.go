package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/ui"
)

// Exercise the subprocess boundary: a kubeadm failure must keep its evidence,
// then remove only the newly-created cluster. A collision must do neither.
func TestFailedKindStartup(t *testing.T) {
	for _, scenario := range []string{"bootstrap", "export-fails", "existing", "concurrent-collision"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "docker")
			t.Setenv("SCENARIO", scenario)
			trace := filepath.Join(dir, "trace")
			t.Setenv("TRACE", trace)
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("docker", "exit 0\n")
			write("kind", `echo "$*" >> "$TRACE"
case "$1 $2" in
  'get clusters')
    if [ "$SCENARIO" = existing ]; then echo astro-startup-test; fi
    exit 0 ;;
  'create cluster')
    if [ "$SCENARIO" = concurrent-collision ]; then
      echo 'ERROR: node(s) already exist for a cluster with the name "astro-startup-test"' >&2
    else
      echo 'kubeadm bootstrap timeout' >&2
    fi
    exit 1 ;;
  'export logs')
    if [ "$SCENARIO" = export-fails ]; then exit 1; fi
    mkdir -p "$3"
    echo 'control-plane evidence' > "$3/kubelet.log"
    exit 0 ;;
  'delete cluster') exit 0 ;;
esac
exit 1
`)
			err := CreateKindCluster("astro-startup-test", nil, ui.Discard())
			if err == nil {
				t.Fatal("startup failure lost")
			}
			data, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			commands := string(data)
			if scenario == "existing" {
				if commands != "get clusters\n" {
					t.Fatalf("pre-existing cluster touched: %s", commands)
				}
				return
			}
			if !strings.Contains(commands, "create cluster --name astro-startup-test --retain") {
				t.Fatalf("missing retained creation: %s", commands)
			}
			if scenario == "concurrent-collision" {
				if strings.Contains(commands, "export logs") || strings.Contains(commands, "delete cluster") {
					t.Fatalf("concurrently-created cluster touched: %s", commands)
				}
				return
			}
			exportAt := strings.Index(commands, "export logs ")
			deleteAt := strings.Index(commands, "delete cluster --name astro-startup-test")
			if exportAt < 0 || deleteAt < exportAt {
				t.Fatalf("expected export before scoped cleanup: %s", commands)
			}
			if scenario == "bootstrap" {
				files, err := filepath.Glob(filepath.Join(dir, ".astrona", "diagnostics", "*", "kind-logs", "kubelet.log"))
				if err != nil || len(files) != 1 {
					t.Fatalf("startup evidence not preserved: %v %v", files, err)
				}
			}
		})
	}
}

func TestKindCollisionOutputAcrossWrites(t *testing.T) {
	var out kindCollisionOutput
	for _, s := range []string{"ERROR: node(s) already exist ", "for a cluster with the name \"x\""} {
		if _, err := out.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if !out.exists {
		t.Fatal("missed collision split across output chunks")
	}
}
