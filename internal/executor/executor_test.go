package executor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKubeconfigEnv(t *testing.T) {
	if env := KubeconfigEnv(""); env != nil {
		t.Fatalf("empty kubeconfig should inherit the environment (nil), got %d vars", len(env))
	}
	env := KubeconfigEnv("/tmp/lab-kubeconfig")
	if last := env[len(env)-1]; last != "KUBECONFIG=/tmp/lab-kubeconfig" {
		t.Fatalf("last env var = %q", last)
	}
}

func TestLocalExecutorExportsKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "/home/user/.kube/config")
	script := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(script, []byte("echo \"$KUBECONFIG\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := (LocalExecutor{Kubeconfig: "/lab/kubeconfig"}).RunScript(script, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "/lab/kubeconfig" {
		t.Fatalf("script saw KUBECONFIG=%q, want the lab's", got)
	}

	out.Reset()
	if err := (LocalExecutor{}).RunScript(script, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "/home/user/.kube/config" {
		t.Fatalf("without a lab kubeconfig, script saw %q, want the inherited one", got)
	}
}
