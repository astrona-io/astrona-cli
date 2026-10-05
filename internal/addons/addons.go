// Package addons installs the well-known cluster components a kind lab can
// enable in runtime.kind.addons (Calico, cert-manager, metrics-server,
// Gateway API via Envoy Gateway).
//
// Every addon is pinned to one upstream release manifest whose SHA-256 is
// compiled in below. A manifest is downloaded over HTTPS once, verified,
// cached under ~/.astrona/cache/addons/<sha256>.yaml (re-verified on every
// use), and applied with `kubectl apply --server-side` — a lab config only
// chooses *which* addons to install, never *what* gets applied. Bumping a
// version means updating URL and SHA256 together in this file.
package addons

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/manifests"
	"astrona/internal/ui"
)

// maxManifestBytes bounds a downloaded addon manifest (Envoy Gateway's,
// CRDs included, is ~4 MB).
const maxManifestBytes = 16 * 1024 * 1024

type manifest struct {
	URL    string
	SHA256 string
}

// addon is one installable component.
type addon struct {
	Name     string
	Version  string
	Manifest manifest
	// After runs once the manifest is applied, before Ready is waited on
	// (extra patches/resources astrona layers on top).
	After func(in *installer) error
	// Ready gates the addon's install; the next addon only starts once
	// they all pass.
	Ready []config.WaitFor
}

var calico = addon{
	Name:    "Calico CNI",
	Version: "v3.32.2",
	Manifest: manifest{
		URL:    "https://raw.githubusercontent.com/projectcalico/calico/v3.32.2/manifests/calico.yaml",
		SHA256: "a8c828a06a87c629a282ebbc424895b77f3a030251993e41ea400a743675bb02",
	},
	Ready: []config.WaitFor{
		{Resource: "ds/calico-node", Namespace: "kube-system", Timeout: "5m"},
		{Resource: "deploy/calico-kube-controllers", Namespace: "kube-system", Timeout: "5m"},
		{Resource: "nodes", All: true, Timeout: "3m"},
	},
}

var certManager = addon{
	Name:    "cert-manager",
	Version: "v1.21.2",
	Manifest: manifest{
		URL:    "https://github.com/cert-manager/cert-manager/releases/download/v1.21.2/cert-manager.yaml",
		SHA256: "e03b668ec8675214af6b0a671699d088f2601fa3878e0dbe1b41d3feafd1879f",
	},
	Ready: []config.WaitFor{
		{Resource: "deploy/cert-manager", Namespace: "cert-manager", Timeout: "3m"},
		{Resource: "deploy/cert-manager-cainjector", Namespace: "cert-manager", Timeout: "3m"},
		{Resource: "deploy/cert-manager-webhook", Namespace: "cert-manager", Timeout: "3m"},
	},
}

var metricsServer = addon{
	Name:    "metrics-server",
	Version: "v0.9.0",
	Manifest: manifest{
		URL:    "https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml",
		SHA256: "1cec29a5267809306a2c6ec74a3e449abbb705b4a8beed0c8a1963910f72c79b",
	},
	// kind's kubelets serve self-signed certificates metrics-server can't
	// verify; this is the standard (local-only) kind workaround.
	After: func(in *installer) error {
		return in.kubectl("-n", "kube-system", "patch", "deployment", "metrics-server", "--type=json",
			`-p=[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]`)
	},
	Ready: []config.WaitFor{
		{Resource: "deploy/metrics-server", Namespace: "kube-system", Timeout: "3m"},
		{Resource: "apiservice/v1beta1.metrics.k8s.io", Condition: "Available", Timeout: "2m"},
	},
}

// GatewayClassName is the GatewayClass labs reference from their Gateways.
const GatewayClassName = "eg"

var envoyGateway = addon{
	Name:    "Envoy Gateway (Gateway API)",
	Version: "v1.9.2",
	Manifest: manifest{
		URL:    "https://github.com/envoyproxy/gateway/releases/download/v1.9.2/install.yaml",
		SHA256: "0412a72907e57ff9b73c56a7bf6df5190bf0f6e4f8bb4bba34e38630bbab5778",
	},
	After: func(in *installer) error {
		// The controller and its CRDs must be up before the resources
		// below can be accepted.
		if err := manifests.WaitFor([]config.WaitFor{
			{Resource: "deploy/envoy-gateway", Namespace: "envoy-gateway-system", Timeout: "3m"},
			{Resource: "crd/envoyproxies.gateway.envoyproxy.io"},
			{Resource: "crd/gatewayclasses.gateway.networking.k8s.io"},
		}, in.kubeContext, in.rep); err != nil {
			return err
		}
		return in.applyBytes("gateway class", []byte(gatewayResources))
	},
	Ready: []config.WaitFor{
		{Resource: "gatewayclass/" + GatewayClassName, Condition: "Accepted", Timeout: "2m"},
	},
}

// gatewayResources is what astrona layers on top of Envoy Gateway:
//
//   - GatewayClass "eg", parameterized with mergeGateways so every Gateway
//     of the class shares one Envoy deployment, and a ClusterIP (not
//     LoadBalancer) Envoy Service: kind has no load balancer, and a
//     LoadBalancer Service that never gets an address keeps every Gateway
//     from ever reporting Programmed;
//   - a NodePort Service in front of that shared Envoy with fixed node
//     ports, which kind maps to 127.0.0.1:<gatewayPorts> on the host
//     (cluster.GatewayNodePortHTTP/HTTPS). Envoy Gateway binds a listener
//     on port N < 1024 to container port N+10000, hence 10080/10443.
//
// It exists before any Gateway does — until a lab creates one, the host
// ports just refuse connections.
var gatewayResources = fmt.Sprintf(`apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: astrona
  namespace: envoy-gateway-system
spec:
  mergeGateways: true
  provider:
    type: Kubernetes
    kubernetes:
      envoyService:
        type: ClusterIP
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: %[1]s
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
  parametersRef:
    group: gateway.envoyproxy.io
    kind: EnvoyProxy
    name: astrona
    namespace: envoy-gateway-system
---
apiVersion: v1
kind: Service
metadata:
  name: astrona-gateway
  namespace: envoy-gateway-system
spec:
  type: NodePort
  selector:
    app.kubernetes.io/managed-by: envoy-gateway
    app.kubernetes.io/component: proxy
    gateway.envoyproxy.io/owning-gatewayclass: %[1]s
  ports:
    - name: http
      port: 80
      targetPort: 10080
      nodePort: %[2]d
    - name: https
      port: 443
      targetPort: 10443
      nodePort: %[3]d
`, GatewayClassName, cluster.GatewayNodePortHTTP, cluster.GatewayNodePortHTTPS)

// selected returns the addons a enables, in install order: the CNI first
// (nothing schedules without one), the gateway last.
func selected(a config.KindAddons) []addon {
	var out []addon
	if a.CNI == config.CNICalico {
		out = append(out, calico)
	}
	if a.CertManager {
		out = append(out, certManager)
	}
	if a.MetricsServer {
		out = append(out, metricsServer)
	}
	if a.GatewayAPI == config.GatewayEnvoy {
		out = append(out, envoyGateway)
	}
	return out
}

type installer struct {
	kubectlPath string
	kubeContext string
	rep         *ui.Reporter
	out         io.Writer
}

func (in *installer) kubectl(args ...string) error {
	cmd := exec.Command(in.kubectlPath, append([]string{"--context", in.kubeContext}, args...)...)
	var stderr bytes.Buffer
	cmd.Stdout = in.out
	cmd.Stderr = io.MultiWriter(in.out, &stderr)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl %s: %w: %s", args[0], err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}

// apply server-side applies path. Server-side because several addon CRDs
// are too large for client-side apply's last-applied annotation.
func (in *installer) apply(path string) error {
	return in.kubectl("apply", "--server-side", "--force-conflicts", "--field-manager=astrona", "-f", path)
}

func (in *installer) applyBytes(what string, data []byte) error {
	f, err := os.CreateTemp("", "astrona-addon-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to write %s manifest: %w", what, err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("failed to write %s manifest: %w", what, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to write %s manifest: %w", what, err)
	}
	return in.apply(f.Name())
}

// Install installs every addon a enables, in order, into the cluster
// behind kubeContext. It stops at the first failure.
func Install(a config.KindAddons, kubeContext string, rep *ui.Reporter) error {
	list := selected(a)
	if len(list) == 0 {
		return nil
	}
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	for _, ad := range list {
		t := rep.Step("Install %s %s", ad.Name, ad.Version)
		in := &installer{kubectlPath: kubectlPath, kubeContext: kubeContext, rep: rep, out: t.Output()}

		path, err := fetch(ad.Manifest, t.Output())
		if err != nil {
			return t.Fail(fmt.Errorf("%s: %w", ad.Name, err))
		}
		if err := in.apply(path); err != nil {
			return t.Fail(fmt.Errorf("%s: %w", ad.Name, err))
		}
		t.Done()

		if ad.After != nil {
			if err := ad.After(in); err != nil {
				return fmt.Errorf("%s: %w", ad.Name, err)
			}
		}
		if err := manifests.WaitFor(ad.Ready, kubeContext, rep); err != nil {
			return fmt.Errorf("%s did not become ready: %w", ad.Name, err)
		}
	}
	return nil
}

// cacheDir is ~/.astrona/cache/addons (created 0700).
func cacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	dir := filepath.Join(home, ".astrona", "cache", "addons")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create addon cache '%s': %w", dir, err)
	}
	return dir, nil
}

var errChecksum = errors.New("checksum mismatch")

func verify(data []byte, want string) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%w: got sha256:%s, want sha256:%s", errChecksum, got, want)
	}
	return nil
}

// fetch returns a local path to m's verified content: the cached copy if
// it still verifies, otherwise a fresh download that must verify before
// it's cached. A cached file that no longer matches is discarded.
func fetch(m manifest, log io.Writer) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	cached := filepath.Join(dir, m.SHA256+".yaml")

	if data, err := os.ReadFile(cached); err == nil {
		if verify(data, m.SHA256) == nil {
			fmt.Fprintf(log, "using cached %s\n", cached)
			return cached, nil
		}
		fmt.Fprintf(log, "cached %s failed verification, downloading again\n", cached)
		os.Remove(cached)
	}

	fmt.Fprintf(log, "downloading %s\n", m.URL)
	tmp, cleanup, err := config.DownloadToTemp(m.URL, "astrona-addon-*.yaml", maxManifestBytes)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", m.URL, err)
	}
	defer cleanup()

	data, err := os.ReadFile(tmp)
	if err != nil {
		return "", fmt.Errorf("read downloaded manifest: %w", err)
	}
	if err := verify(data, m.SHA256); err != nil {
		return "", fmt.Errorf("refusing to apply %s: %w", m.URL, err)
	}

	f, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return "", fmt.Errorf("cache manifest: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", fmt.Errorf("cache manifest: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("cache manifest: %w", err)
	}
	if err := os.Rename(f.Name(), cached); err != nil {
		return "", fmt.Errorf("cache manifest: %w", err)
	}
	return cached, nil
}
