package labshell

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
)

func kubeFixture(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: kind-%[1]s
clusters:
- name: kind-%[1]s
  cluster:
    server: https://127.0.0.1:12345
    certificate-authority-data: Q0E=
    proxy-url: http://host-secret-proxy
users:
- name: kind-%[1]s
  user:
    client-certificate-data: Q0VSVA==
    client-key-data: S0VZ
    exec:
      command: /host-secret-script
contexts:
- name: kind-%[1]s
  context:
    cluster: kind-%[1]s
    user: kind-%[1]s
`, name)
}

func TestContainerCredentials(t *testing.T) {
	data, err := containerKubeconfig("astro-lab", []byte(kubeFixture("astro-lab")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://astro-lab-control-plane:6443") {
		t.Fatal(string(data))
	}
	for _, bad := range []string{"127.0.0.1", "host-secret", "exec:", "proxy-url"} {
		if strings.Contains(string(data), bad) {
			t.Fatalf("copied %s", bad)
		}
	}
	for _, bad := range []string{strings.ReplaceAll(kubeFixture("astro-lab"), "Q0VSVA==", ""), kubeFixture("other"), "{}"} {
		if _, err := containerKubeconfig("astro-lab", []byte(bad)); err == nil {
			t.Fatal("accepted unrelated or incomplete credentials")
		}
	}
}

func TestImagePreparationFailure(t *testing.T) {
	engine := filepath.Join(t.TempDir(), "engine")
	if err := os.WriteFile(engine, []byte("#!/bin/sh\necho unavailable >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := ensureImage(t.Context(), engine); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("image failure was hidden: %v", err)
	}
	imageBuild <- struct{}{}
	defer func() { <-imageBuild }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ensureImage(ctx, engine); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting for another build ignored cancellation: %v", err)
	}
}

func TestWorkspaceRejectsSymlinks(t *testing.T) {
	for _, part := range []string{apphome.Name(), "runtime", "astro-lab", "work", "config"} {
		t.Run(part, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			parts := []string{apphome.Name(), "runtime", "astro-lab", part}
			for i, p := range parts {
				if p == part {
					parts = parts[:i+1]
					break
				}
			}
			path := filepath.Join(append([]string{home}, parts...)...)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
			if root, _, err := prepareWorkspace("astro-lab"); err == nil {
				root.Close()
				t.Fatal("accepted symlink mount source")
			}
		})
	}
}

func TestWorkspaceCleanupDoesNotFollowLinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, path, err := prepareWorkspace("astro-lab")
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	out := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(out, []byte("host data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(out), filepath.Join(path, "work", "outside")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(t.Context(), "astro-lab"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "host data" {
		t.Fatal("cleanup changed host files")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("workspace remains")
	}
	if _, _, err := prepareWorkspace("../outside"); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestTerminalContainerArguments(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		args, err := runArgs(engine, "/safe/path", "astro-lab", "tab", []string{"HOME=/workspace"}, []string{"idp.astrona.internal:10.0.0.2"}, []string{"sh", "-c", "echo hello"}, true)
		if err != nil {
			t.Fatal(err)
		}
		mountOptions := ""
		if engine == "podman" {
			mountOptions = ",relabel=shared"
		}
		for _, required := range []string{"--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only", "--pids-limit=256", "--tty", "kind", "idp.astrona.internal:10.0.0.2", "type=bind,source=/safe/path/work,target=/workspace" + mountOptions, "type=bind,source=/safe/path/config,target=/run/astrona,readonly" + mountOptions} {
			if !slices.Contains(args, required) {
				t.Fatalf("missing %s", required)
			}
		}
		for _, bad := range []string{"--privileged", "--network=host", "--pid=host", "docker.sock", "SSH_AUTH_SOCK", "/Users/"} {
			if strings.Contains(strings.Join(args, " "), bad) {
				t.Fatalf("unsafe argument %s", bad)
			}
		}
		if engine == "podman" && !slices.Contains(args, "--userns=keep-id") {
			t.Fatal("rootless workspace ownership missing")
		}
	}
	if _, err := runArgs("docker", "/path,readonly=false", "lab", "tab", nil, nil, nil, false); err == nil {
		t.Fatal("accepted mount option injection")
	}
}

func TestWorkspaceKeepsContextEdits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, path, err := prepareWorkspace("astro-lab")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source, err := cluster.KubeconfigPath("astro-lab")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(kubeFixture("astro-lab")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyKubeconfig(root, "astro-lab"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "work", ".kube", "astro-lab.yaml")
	if err := os.WriteFile(file, []byte("user namespace edits"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyKubeconfig(root, "astro-lab"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "user namespace edits" {
		t.Fatal("new tab overwrote working kubeconfig")
	}
}

var containerSmoke = flag.Bool("container-smoke", false, "run isolated-terminal checks with a disposable Kind cluster")

func TestContainerIsolation(t *testing.T) {
	if !*containerSmoke {
		t.Skip("requires Docker/Podman and kind; enable -container-smoke")
	}
	originalHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("XDG_CONFIG_HOME") == "" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(originalHome, ".config"))
	}
	if os.Getenv("DOCKER_CONFIG") == "" {
		t.Setenv("DOCKER_CONFIG", filepath.Join(originalHome, ".docker"))
	}
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	engine, err := cluster.DetectContainerEngine()
	if err != nil {
		t.Fatal(err)
	}
	// Use a throwaway kubeconfig and profile; never change the user's context.
	home := t.TempDir()
	name := apphome.TestClusterPrefix() + fmt.Sprint(time.Now().UnixNano())
	global := filepath.Join(home, "kind.yaml")
	args := []string{"create", "cluster", "--name", name, "--kubeconfig", global, "--wait", "90s"}
	if image := os.Getenv("ASTRONA_TEST_NODE_IMAGE"); image != "" {
		args = append(args, "--image", image)
	}
	create := exec.CommandContext(ctx, "kind", args...)
	create.Env = append(os.Environ(), "KIND_EXPERIMENTAL_PROVIDER="+engine.Name)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create smoke cluster: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(cleanup, "kind", "delete", "cluster", "--name", name, "--kubeconfig", global)
		cmd.Env = append(os.Environ(), "KIND_EXPERIMENTAL_PROVIDER="+engine.Name)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("remove smoke cluster: %v\n%s", err, out)
		}
	})
	// Save only this disposable cluster's credentials in the temporary profile.
	path, err := cluster.KubeconfigPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(global)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := Remove(context.Background(), name); err != nil {
			t.Error(err)
		}
		_ = os.RemoveAll(filepath.Dir(path))
	})
	sentinel := filepath.Join(home, "host-secret")
	if err := os.WriteFile(sentinel, []byte("must stay private"), 0600); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
test "$PWD" = /workspace
test "$HOME" = /workspace
test ! -e "$1"
test ! -S /var/run/docker.sock
test -z "${SSH_AUTH_SOCK:-}"
ln -s "$1" escape
! cat escape
! sh -c 'echo wrong > /etc/host-isolation-probe'
echo shared > work.txt
kubectl get namespaces -o name
kubectl config set-context --current --namespace=kube-system
printf ISOLATED_OK
`
	cmd, err := Command(ctx, name, name, []string{"sh", "-c", script, "smoke", sentinel}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Cancel() }()
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "ISOLATED_OK") {
		t.Fatalf("isolated terminal: %v\n%s", err, out)
	}
	second, err := Command(ctx, name, name, []string{"sh", "-c", `test "$(cat work.txt)" = shared && kubectl config view --minify -o 'jsonpath={..namespace}'`}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Cancel() }()
	if out, err := second.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "kube-system" {
		t.Fatalf("reopen workspace: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "must stay private" {
		t.Fatal("host sentinel changed")
	}
}
