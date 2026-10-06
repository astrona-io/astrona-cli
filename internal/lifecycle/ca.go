package lifecycle

import (
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/runtime"
	"astrona/internal/ui"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// webhookRetryFor bounds waiting for cert-manager's webhook to serve.
const webhookRetryFor = 90 * time.Second

// webhookNotReady recognizes kubectl's error for an admission webhook that
// isn't serving yet.
func webhookNotReady(out string) bool {
	return strings.Contains(out, "failed calling webhook") &&
		(strings.Contains(out, "connection refused") || strings.Contains(out, "no endpoints available") || strings.Contains(out, "context deadline exceeded"))
}

// CAEnvVar points host scripts, command checks and `astrona shell` at the
// lab CA's certificate.
const CAEnvVar = "ASTRONA_CA_CERT"

// PrepareSharedCA creates the lab's CA (runtime.kind.sharedCA) before any
// of its clusters, and records which lab's CA they install. No-op without
// sharedCA.
func PrepareSharedCA(cfg *config.LabConfig, labCluster string) error {
	k := cfg.Runtime.Kind
	if k == nil || !k.SharedCA {
		return nil
	}
	if _, err := cluster.EnsureLabCA(labCluster); err != nil {
		return fmt.Errorf("lab CA: %w", err)
	}
	kc := *k
	kc.CALab = labCluster
	cfg.Runtime.Kind = &kc
	return nil
}

// InstallSharedCA puts the lab CA into env's freshly created cluster —
// after addons (cert-manager must be up for the ClusterIssuer), before
// bootstrap — and points host scripts at its certificate:
//   - ConfigMap astrona-ca (ca.crt) in default: what pods mount to trust it
//   - Secret astrona-ca (kubernetes.io/tls) — in cert-manager with a
//     ClusterIssuer astrona-ca when the cert-manager addon is on, in
//     default otherwise
//
// The key goes to kubectl on stdin, never on its command line.
func InstallSharedCA(cfg *config.LabConfig, env *runtime.LabEnvironment, rep *ui.Reporter) error {
	k := cfg.Runtime.Kind
	if env.Type != runtime.RuntimeKind || k == nil || !k.SharedCA || k.CALab == "" {
		return nil
	}
	ca, ok := cluster.ExistingLabCA(k.CALab)
	if !ok {
		return fmt.Errorf("lab CA of %s is missing", k.CALab)
	}
	t := rep.Step("Install lab CA (ConfigMap/Secret astrona-ca)")
	docs, err := sharedCAManifests(ca, k.Addons.CertManager)
	if err != nil {
		return t.Fail(err)
	}
	// cert-manager's webhook can report rolled out a little before it
	// serves, which rejects the ClusterIssuer — retry only that.
	deadline := time.Now().Add(webhookRetryFor)
	for {
		var out bytes.Buffer
		cmd := exec.Command("kubectl", "--context", env.KubeContext, "apply", "-f", "-")
		cmd.Env = executor.KubeconfigEnv(env.Kubeconfig)
		cmd.Stdin = bytes.NewReader(docs)
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		t.Output().Write(out.Bytes())
		if err == nil {
			break
		}
		if !webhookNotReady(out.String()) || time.Now().After(deadline) {
			return t.Fail(fmt.Errorf("apply lab CA: %w", err))
		}
		time.Sleep(3 * time.Second)
	}
	t.Done()
	env.AddEnv(CAEnvVar + "=" + ca.CertPath)
	return nil
}

func sharedCAManifests(ca cluster.LabCA, certManager bool) ([]byte, error) {
	crt, err := os.ReadFile(ca.CertPath)
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(ca.KeyPath)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{"app.kubernetes.io/managed-by": "astrona"}
	secretNS := "default"
	if certManager {
		secretNS = "cert-manager"
	}
	objs := []any{
		map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "astrona-ca", "namespace": "default", "labels": labels},
			"data":     map[string]string{"ca.crt": string(crt)},
		},
		map[string]any{
			"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls",
			"metadata":   map[string]any{"name": "astrona-ca", "namespace": secretNS, "labels": labels},
			"stringData": map[string]string{"tls.crt": string(crt), "tls.key": string(key)},
		},
	}
	if certManager {
		objs = append(objs, map[string]any{
			"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
			"metadata": map[string]any{"name": "astrona-ca", "labels": labels},
			"spec":     map[string]any{"ca": map[string]string{"secretName": "astrona-ca"}},
		})
	}
	var out bytes.Buffer
	for i, o := range objs {
		if i > 0 {
			out.WriteString("---\n")
		}
		b, err := yaml.Marshal(o)
		if err != nil {
			return nil, err
		}
		out.Write(b)
	}
	return out.Bytes(), nil
}

// CAEnv is the CA variable for a running lab with a shared CA.
func CAEnv(lab string) []string {
	if ca, ok := cluster.ExistingLabCA(lab); ok {
		return []string{CAEnvVar + "=" + ca.CertPath}
	}
	return nil
}
