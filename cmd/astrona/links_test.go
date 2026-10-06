package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/ui"

	"gopkg.in/yaml.v3"
)

func writeLab(t *testing.T, dir, body string) {
	t.Helper()
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLink(t *testing.T) {
	root := t.TempDir()
	writeLab(t, filepath.Join(root, "idp-lab"), "metadata: {name: idp-lab}\n")
	appDir := filepath.Join(root, "app-lab")
	writeLab(t, appDir, "metadata: {name: app}\n")

	ll, err := resolveLink(config.Link{Name: "idp", Lab: "../idp-lab"}, appDir, &rootFlags{fileName: "custom.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if ll.cluster != "astro-idp-lab" || ll.cfg == nil || ll.flags.fileName != "config.yaml" {
		t.Fatalf("path link = %+v (the linked lab must use its own config.yaml)", ll)
	}

	byName, _ := resolveLink(config.Link{Name: "db", Lab: "shared-db"}, appDir, &rootFlags{})
	if byName.cluster != "astro-shared-db" || byName.cfg != nil {
		t.Fatalf("name link = %+v", byName)
	}

	if _, err := resolveLink(config.Link{Name: "x", Lab: "../idp-lab"}, "https:/labs.example/app", &rootFlags{}); err == nil {
		t.Error("URL config linked by path")
	}

	writeLab(t, filepath.Join(root, "vm-lab"), "metadata: {name: vm}\nruntime:\n  type: qemu\n  qemu: [{image: {type: file, source: x}}]\n")
	if _, err := resolveLink(config.Link{Name: "vm", Lab: "../vm-lab"}, appDir, &rootFlags{}); err == nil || !strings.Contains(err.Error(), "only kind labs") {
		t.Errorf("qemu link = %v", err)
	}
}

func TestResolveLinkStaysInsideGitRepo(t *testing.T) {
	outside := t.TempDir()
	writeLab(t, filepath.Join(outside, "secret-lab"), "metadata: {name: secret}\n")
	repo := filepath.Join(outside, "repo")
	writeLab(t, filepath.Join(repo, "labs", "app"), "metadata: {name: app}\n")
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	flags := &rootFlags{gitURL: "https://example.com/labs.git", configPath: "labs/app"}
	_, err := resolveLink(config.Link{Name: "s", Lab: "../../../secret-lab"}, filepath.Join(repo, "labs", "app"), flags)
	if err == nil || !strings.Contains(err.Error(), "outside the lab's git repository") {
		t.Fatalf("escape = %v", err)
	}
}

func TestLinkCycleRefusedBeforeStarting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeLab(t, filepath.Join(root, "a"), "metadata: {name: cyc-a}\nlinks: [{name: b, lab: ../b}]\n")
	writeLab(t, filepath.Join(root, "b"), "metadata: {name: cyc-b}\nlinks: [{name: a, lab: ../a}]\n")
	cfg, _, cleanup, err := LoadLabForCommand(&rootFlags{configPath: filepath.Join(root, "a"), fileName: "config.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	_, err = startLinkedLabs(cfg, filepath.Join(root, "a"), &rootFlags{}, ui.Discard(), map[string]bool{"astro-cyc-a": true}, 0)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle = %v", err)
	}
}

func TestLinksConfigMap(t *testing.T) {
	data, err := linksConfigMap([]cluster.LinkState{{Name: "idp", Cluster: "astro-idp-lab"}})
	if err != nil {
		t.Fatal(err)
	}
	var cm struct {
		Kind     string
		Metadata struct{ Name, Namespace string }
		Data     map[string]string
	}
	if err := yaml.Unmarshal(data, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.Kind != "ConfigMap" || cm.Metadata.Name != "astrona-links" || cm.Data["idp.host"] != "astro-idp-lab-control-plane" || cm.Data["idp.context"] != "kind-astro-idp-lab" {
		t.Fatalf("configmap = %+v", cm)
	}
}
