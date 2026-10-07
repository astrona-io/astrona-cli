package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/ui"
)

// Every kind lab gets its own kubeconfig, ~/.astrona/kind/<lab>/kubeconfig,
// holding only that cluster with its context as current-context. astrona
// runs lab scripts and `command` checks with KUBECONFIG pointed at it, and
// `astrona shell` hands it to the student.
//
// kind still merges the cluster into the user's own kubeconfig too (so
// `kubectl --context kind-<lab>` keeps working), but astrona restores the
// user's current-context right after `kind create` — a lab is never made
// current mid-build. Only once the lab is ready does `astrona run`
// deliberately switch to it (KubectlContexts, labstate.SwitchContext),
// and `astrona destroy` switches back.

const kubeconfigFile = "kubeconfig"

// KindStateDir returns ~/.astrona/kind (not created).
func KindStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".astrona", "kind"), nil
}

func labStateDir(lab string) (string, error) {
	// Keeps the <lab> path component from escaping ~/.astrona/kind.
	if err := config.ValidateName(lab); err != nil {
		return "", fmt.Errorf("lab name: %w", err)
	}
	base, err := KindStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, lab), nil
}

// KubeconfigPath is where lab's isolated kubeconfig lives (whether or not
// it exists).
func KubeconfigPath(lab string) (string, error) {
	dir, err := labStateDir(lab)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, kubeconfigFile), nil
}

// ExistingKubeconfig returns lab's isolated kubeconfig path, or "" if it
// has none — e.g. a lab created by an astrona version before isolation
// existed, which then keeps using the user's default kubeconfig.
func ExistingKubeconfig(lab string) string {
	path, err := KubeconfigPath(lab)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// WriteLabKubeconfig exports clusterName's kubeconfig (`kind get
// kubeconfig`) into its isolated file, 0600 in a 0700 dir — it holds the
// cluster's admin client key.
func WriteLabKubeconfig(clusterName string, rep *ui.Reporter) (string, error) {
	t := rep.Step("Write lab kubeconfig")

	path, err := KubeconfigPath(clusterName)
	if err != nil {
		return "", t.Fail(err)
	}
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return "", t.Fail(fmt.Errorf("kind not found in PATH: %w", err))
	}

	var stdout bytes.Buffer
	cmd := exec.Command(kindPath, "get", "kubeconfig", "--name", clusterName)
	cmd.Env = kindEnv()
	cmd.Stdout = &stdout
	cmd.Stderr = t.Output()
	if err := cmd.Run(); err != nil {
		return "", t.Fail(fmt.Errorf("kind get kubeconfig failed: %w", err))
	}
	if stdout.Len() == 0 {
		return "", t.Fail(fmt.Errorf("kind get kubeconfig returned nothing for '%s'", clusterName))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", t.Fail(fmt.Errorf("failed to create '%s': %w", filepath.Dir(path), err))
	}
	if err := writeFileAtomic(path, stdout.Bytes(), 0600); err != nil {
		return "", t.Fail(err)
	}

	t.Done()
	return path, nil
}

// RemoveLabState deletes ~/.astrona/kind/<lab> (its kubeconfig). Missing is
// fine.
func RemoveLabState(lab string) error {
	dir, err := labStateDir(lab)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to remove '%s': %w", dir, err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("failed to write '%s': %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write '%s': %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write '%s': %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write '%s': %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to write '%s': %w", path, err)
	}
	return nil
}

// kindEnv is the environment kind runs with: the user's own, plus the
// podman provider switch when podman is the engine.
func kindEnv() []string {
	env := os.Environ()
	if engine, err := DetectContainerEngine(); err == nil && engine.Name == "podman" {
		env = append(env, "KIND_EXPERIMENTAL_PROVIDER=podman")
	}
	return env
}

// errNoCurrentContext marks "the kubeconfig has no current-context" — a
// valid prior state to restore, distinct from "couldn't find out".
var errNoCurrentContext = errors.New("no current-context set")

func currentContext(kubectlPath string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(kubectlPath, "config", "current-context")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if strings.Contains(out.String(), "current-context is not set") {
			return "", errNoCurrentContext
		}
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// PreserveCurrentContext records the user's kubectl current-context and
// returns a func that puts it back — call it after `kind create cluster`,
// which always switches current-context to the new cluster. Best effort:
// if the prior context can't be determined (no kubectl, unreadable
// kubeconfig), the returned func only warns, it never touches the
// kubeconfig blindly.
func PreserveCurrentContext(rep *ui.Reporter) func() {
	kubectlPath, err := executor.LookKubectl()
	if err != nil {
		return func() {
			rep.Warn("kubectl not found — kind may have switched your kubectl current-context to the new lab")
		}
	}

	prev, err := currentContext(kubectlPath)
	hadNone := errors.Is(err, errNoCurrentContext)
	if err != nil && !hadNone {
		return func() {
			rep.Warn("could not read your kubectl current-context before creating the lab (%s) — kind may have switched it", err)
		}
	}

	return func() {
		now, err := currentContext(kubectlPath)
		if err == nil && now == prev {
			return
		}

		var cmd *exec.Cmd
		if hadNone {
			cmd = exec.Command(kubectlPath, "config", "unset", "current-context")
		} else {
			cmd = exec.Command(kubectlPath, "config", "use-context", prev)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			rep.Warn("could not restore your kubectl current-context to '%s': %s", prev, strings.TrimSpace(string(out)))
			return
		}
		if hadNone {
			rep.Info("Left your kubectl current-context unset, as it was before.")
		} else {
			rep.Info("Restored your kubectl current-context to '%s'.", prev)
		}
	}
}

// defaultSAPollInterval is how often WaitForDefaultServiceAccount checks.
var defaultSAPollInterval = time.Second

// WaitForDefaultServiceAccount blocks until the "default" namespace's
// default ServiceAccount exists. `kind create cluster` returns before the
// controller-manager has created it, and until it does, creating any Pod
// in that namespace is rejected ("serviceaccount \"default\" not found") —
// a race that makes a lab's very first `kubectl apply` fail intermittently.
func WaitForDefaultServiceAccount(kubeContext, kubeconfig string, timeout time.Duration, rep *ui.Reporter) error {
	t := rep.Step("Wait for cluster API to be ready")
	kubectlPath, err := executor.LookKubectl()
	if err != nil {
		return t.Fail(err)
	}

	deadline := time.Now().Add(timeout)
	var last []byte
	for {
		cmd := exec.Command(kubectlPath, "--context", kubeContext, "--namespace", "default", "get", "serviceaccount", "default", "-o", "name")
		if kubeconfig != "" {
			cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
		}
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Done()
			return nil
		}
		last = out
		if time.Now().After(deadline) {
			return t.Fail(fmt.Errorf("default service account not created within %s: %s", timeout, strings.TrimSpace(string(last))))
		}
		time.Sleep(defaultSAPollInterval)
	}
}

// KubectlContexts is the user's own kubeconfig, read and changed through
// kubectl config — so $KUBECONFIG (one file or several) is respected
// exactly as the user's own kubectl respects it. Implements
// labstate.Contexts.
type KubectlContexts struct {
	Path string // kubectl binary
}

// NewKubectlContexts finds kubectl.
func NewKubectlContexts() (KubectlContexts, error) {
	path, err := executor.LookKubectl()
	if err != nil {
		return KubectlContexts{}, err
	}
	return KubectlContexts{Path: path}, nil
}

// Current returns the current-context; set is false when there is none.
func (k KubectlContexts) Current() (string, bool, error) {
	name, err := currentContext(k.Path)
	if errors.Is(err, errNoCurrentContext) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, name != "", nil
}

// Use makes name the current-context.
func (k KubectlContexts) Use(name string) error {
	return k.run("config", "use-context", name)
}

// Unset clears the current-context.
func (k KubectlContexts) Unset() error {
	return k.run("config", "unset", "current-context")
}

// Exists reports whether the kubeconfig has a context called name.
func (k KubectlContexts) Exists(name string) (bool, error) {
	out, err := exec.Command(k.Path, "config", "get-contexts", "-o", "name").Output()
	if err != nil {
		return false, fmt.Errorf("kubectl config get-contexts: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func (k KubectlContexts) run(args ...string) error {
	out, err := exec.Command(k.Path, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
