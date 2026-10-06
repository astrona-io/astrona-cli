package lifecycle

import (
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/ui"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKindClusterConfig(t *testing.T) {
	cfg := &config.LabConfig{
		Metadata: config.MetadataConfig{Name: "auth-lab"},
		Runtime: config.RuntimeConfig{Type: "kind", Kind: &config.KindConfig{Clusters: []config.KindCluster{
			{Name: "idp", PreloadImages: []string{"nginx:1.27-alpine"}, Addons: config.KindAddons{GatewayAPI: "envoy"},
				Bootstrap: config.BootstrapConfig{Manifests: []config.ResourceItem{{Name: "idp", Type: "file", Source: "idp/idp.yaml"}}}},
			{Name: "db"},
		}}},
	}
	sub := LinkedClusterConfig(cfg, cfg.KindClusters()[0])
	if sub.Metadata.Name != "auth-lab-idp" || sub.Runtime.Type != "kind" || len(sub.Runtime.Kind.PreloadImages) != 1 || len(sub.Bootstrap.Manifests) != 1 {
		t.Fatalf("linked cluster config = %+v", sub)
	}
	if !sub.Runtime.Kind.Addons.SkipHostPorts {
		t.Error("a linked cluster's gateway must not take the lab's host ports")
	}
	if len(sub.KindClusters()) != 0 {
		t.Error("a linked cluster must not have linked clusters of its own")
	}

	_, states, err := ClusterStates(cfg, "astro-test-auth-lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0] != (cluster.LinkState{Name: "idp", Cluster: "astro-test-auth-lab-idp"}) || states[1].Cluster != "astro-test-auth-lab-db" {
		t.Fatalf("states = %+v — test copies must get their own cluster names", states)
	}
	if _, none, _ := ClusterStates(&config.LabConfig{}, "astro-x"); none != nil {
		t.Error("lab without linked clusters has states")
	}
}

func TestLinksConfigMap(t *testing.T) {
	data, err := linksConfigMap([]cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}})
	if err != nil {
		t.Fatal(err)
	}
	// astrona-clusters, and the same data under the pre-v0.3 name.
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var names []string
	for {
		var cm struct {
			Kind     string
			Metadata struct{ Name, Namespace string }
			Data     map[string]string
		}
		if err := dec.Decode(&cm); err != nil {
			break
		}
		names = append(names, cm.Metadata.Name)
		if cm.Kind != "ConfigMap" || cm.Data["idp.host"] != "astro-app-idp-control-plane" || cm.Data["idp.hostname"] != "idp.astrona.internal" || cm.Data["idp.context"] != "kind-astro-app-idp" {
			t.Fatalf("configmap = %+v", cm)
		}
	}
	if strings.Join(names, ",") != "astrona-clusters,astrona-links" {
		t.Fatalf("configmaps = %v", names)
	}
}

func TestKindClusterStatesFollowDependencies(t *testing.T) {
	cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Clusters: []config.KindCluster{
		{Name: "app", DependsOn: []string{"db"}},
		{Name: "db"},
	}}}}
	order, states, err := ClusterStates(cfg, "astro-x")
	if err != nil {
		t.Fatal(err)
	}
	if order[0].Name != "db" || states[0].Cluster != "astro-x-db" || states[1].Cluster != "astro-x-app" {
		t.Fatalf("order = %+v, states = %+v — db must come first", order, states)
	}
	if got := notStarted(order[1:]); got != "app, the lab itself" {
		t.Errorf("notStarted = %q", got)
	}
}

// A teardown for an environment that doesn't exist runs on the host — but
// must never inherit the user's own kubeconfig (and so their current
// context).
func TestTeardownFallbackHasNoClusterAccess(t *testing.T) {
	env := TeardownEnvironment("astro-no-such-lab-for-test", config.RuntimeConfig{Type: "kind"}, ui.Discard())
	local, ok := env.Executor.(executor.LocalExecutor)
	if !ok || local.Kubeconfig != os.DevNull {
		t.Fatalf("fallback executor = %#v — want a LocalExecutor with KUBECONFIG=%s", env.Executor, os.DevNull)
	}
}

func TestSharedCAManifestsAndPropagation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ca, err := cluster.EnsureLabCA("astro-ca-lab")
	if err != nil {
		t.Fatal(err)
	}
	with, err := sharedCAManifests(ca, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: ConfigMap", "ca.crt:", "kind: Secret", "type: kubernetes.io/tls", "namespace: cert-manager", "kind: ClusterIssuer", "secretName: astrona-ca"} {
		if !strings.Contains(string(with), want) {
			t.Errorf("with cert-manager: missing %q", want)
		}
	}
	without, _ := sharedCAManifests(ca, false)
	if strings.Contains(string(without), "ClusterIssuer") || strings.Contains(string(without), "namespace: cert-manager") {
		t.Errorf("without cert-manager:\n%s", without)
	}

	cfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: "ca-lab"}, Runtime: config.RuntimeConfig{Kind: &config.KindConfig{SharedCA: true, Clusters: []config.KindCluster{{Name: "idp"}}}}}
	if err := PrepareSharedCA(cfg, "astro-ca-lab"); err != nil {
		t.Fatal(err)
	}
	sub := LinkedClusterConfig(cfg, cfg.KindClusters()[0])
	if !sub.Runtime.Kind.SharedCA || sub.Runtime.Kind.CALab != "astro-ca-lab" {
		t.Errorf("linked cluster doesn't install the lab's CA: %+v", sub.Runtime.Kind)
	}
	if env := CAEnv("astro-ca-lab"); len(env) != 1 || env[0] != CAEnvVar+"="+ca.CertPath {
		t.Errorf("CAEnv = %v", env)
	}
}

func TestWebhookNotReady(t *testing.T) {
	refused := `Error from server (InternalError): error when creating "STDIN": Internal error occurred: failed calling webhook "webhook.cert-manager.io": failed to call webhook: Post "https://cert-manager-webhook.cert-manager.svc:443/validate?timeout=30s": dial tcp 10.96.149.246:443: connect: connection refused`
	if !webhookNotReady(refused) {
		t.Error("webhook not serving yet should be retried")
	}
	if webhookNotReady(`Error from server (BadRequest): error when creating "STDIN": ClusterIssuer in version "v1" cannot be handled`) {
		t.Error("a real error must not be retried")
	}
}
