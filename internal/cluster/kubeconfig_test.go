package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/ui"
)

// fakeKubectl installs a kubectl whose current-context lives in a state
// file ("" = unset), supporting just the subcommands PreserveCurrentContext
// uses. Every invocation is appended to a calls log.
func fakeKubectl(t *testing.T, initial string, haveContext bool) (stateFile, callsFile string) {
	t.Helper()
	dir := t.TempDir()
	stateFile = filepath.Join(dir, "current")
	callsFile = filepath.Join(dir, "calls")
	if haveContext {
		if err := os.WriteFile(stateFile, []byte(initial), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
echo "$*" >> "` + callsFile + `"
case "$*" in
  "config current-context")
    if [ -s "` + stateFile + `" ]; then cat "` + stateFile + `"; echo; else echo "error: current-context is not set" >&2; exit 1; fi ;;
  "config use-context "*) printf '%s' "$3" > "` + stateFile + `" ;;
  "config unset current-context") : > "` + stateFile + `" ;;
  *) echo "unexpected: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stateFile, callsFile
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data))
}

func TestPreserveCurrentContextRestoresPrevious(t *testing.T) {
	state, _ := fakeKubectl(t, "my-prod", true)

	restore := PreserveCurrentContext(ui.Discard())
	os.WriteFile(state, []byte("kind-astro-x"), 0600) // what kind create does
	restore()

	if got := readFile(t, state); got != "my-prod" {
		t.Fatalf("current-context = %q, want my-prod restored", got)
	}
}

func TestPreserveCurrentContextUnsetsWhenNoneBefore(t *testing.T) {
	state, _ := fakeKubectl(t, "", false)

	restore := PreserveCurrentContext(ui.Discard())
	os.WriteFile(state, []byte("kind-astro-x"), 0600)
	restore()

	if got := readFile(t, state); got != "" {
		t.Fatalf("current-context = %q, want unset again", got)
	}
}

func TestPreserveCurrentContextNoopWhenUnchanged(t *testing.T) {
	_, calls := fakeKubectl(t, "my-prod", true)

	restore := PreserveCurrentContext(ui.Discard())
	restore()

	for _, line := range strings.Split(readFile(t, calls), "\n") {
		if strings.HasPrefix(line, "config use-context") || strings.HasPrefix(line, "config unset") {
			t.Fatalf("kubeconfig written although context never changed: %q", line)
		}
	}
}

func TestWriteAndRemoveLabKubeconfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	bin := t.TempDir()
	kind := "#!/bin/sh\n[ \"$1 $2 $3 $4\" = \"get kubeconfig --name astro-x\" ] || exit 3\necho 'apiVersion: v1'\necho 'current-context: kind-astro-x'\n"
	if err := os.WriteFile(filepath.Join(bin, "kind"), []byte(kind), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := ExistingKubeconfig("astro-x"); got != "" {
		t.Fatalf("ExistingKubeconfig before write = %q", got)
	}

	path, err := WriteLabKubeconfig("astro-x", ui.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".astrona", "kind", "astro-x", "kubeconfig"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	if !strings.Contains(readFile(t, path), "current-context: kind-astro-x") {
		t.Fatalf("kubeconfig content wrong: %q", readFile(t, path))
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Errorf("kubeconfig mode = %o, want 600", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0700 {
		t.Errorf("lab dir mode = %o, want 700", info.Mode().Perm())
	}
	if got := ExistingKubeconfig("astro-x"); got != path {
		t.Fatalf("ExistingKubeconfig = %q, want %q", got, path)
	}

	if err := RemoveLabState("astro-x"); err != nil {
		t.Fatal(err)
	}
	if got := ExistingKubeconfig("astro-x"); got != "" {
		t.Fatal("kubeconfig still present after RemoveLabState")
	}
	if err := RemoveLabState("astro-x"); err != nil {
		t.Fatalf("second RemoveLabState: %v", err)
	}
}

// On a podman-only machine (docker installed but not answering), kind
// delete must use the podman provider, like create does.
func TestDeleteKindClusterUsesPodmanProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	envFile := filepath.Join(bin, "kind-env")
	scripts := map[string]string{
		"docker": "#!/bin/sh\nexit 1\n",
		"podman": "#!/bin/sh\nexit 0\n",
		"kind":   "#!/bin/sh\necho \"provider=$KIND_EXPERIMENTAL_PROVIDER\" > \"" + envFile + "\"\n",
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "")

	if err := DeleteKindCluster("astro-x", ui.Discard()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, envFile); got != "provider=podman" {
		t.Fatalf("kind delete ran with %q, want provider=podman", got)
	}
}

func TestKubeconfigPathRejectsUnsafeLabNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, lab := range []string{"", "../x", "a/b", ".hidden", "a..b"} {
		if _, err := KubeconfigPath(lab); err == nil {
			t.Errorf("KubeconfigPath(%q) accepted an unsafe name", lab)
		}
	}
}

func TestWaitForDefaultServiceAccount(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "n")
	// Fails twice ("not found"), then succeeds.
	script := `#!/bin/sh
n=$(( $(cat "` + count + `" 2>/dev/null || echo 0) + 1 )); echo $n > "` + count + `"
if [ $n -lt 3 ]; then echo 'Error from server (NotFound): serviceaccounts "default" not found' >&2; exit 1; fi
echo serviceaccount/default
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := defaultSAPollInterval
	defaultSAPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { defaultSAPollInterval = old })

	if err := WaitForDefaultServiceAccount("kind-x", "", 5*time.Second, ui.Discard()); err != nil {
		t.Fatalf("WaitForDefaultServiceAccount: %v", err)
	}
	if got := readFile(t, count); got != "3" {
		t.Fatalf("attempts = %s, want 3", got)
	}

	os.WriteFile(count, []byte("-100"), 0600) // never reaches success in time
	err := WaitForDefaultServiceAccount("kind-x", "", 50*time.Millisecond, ui.Discard())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("timeout error = %v", err)
	}
}
