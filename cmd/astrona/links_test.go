package main

import (
	"os"
	"strings"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/ui"

	"gopkg.in/yaml.v3"
)

func TestKindLabConfig(t *testing.T) {
	cfg := &config.LabConfig{
		Metadata: config.MetadataConfig{Name: "auth-lab"},
		Runtime: config.RuntimeConfig{Type: "kind", Kind: &config.KindConfig{Labs: []config.KindLab{
			{Name: "idp", PreloadImages: []string{"nginx:1.27-alpine"}, Addons: config.KindAddons{GatewayAPI: "envoy"},
				Bootstrap: config.BootstrapConfig{Manifests: []config.ResourceItem{{Name: "idp", Type: "file", Source: "idp/idp.yaml"}}}},
			{Name: "db"},
		}}},
	}
	sub := kindLabConfig(cfg, cfg.KindLabs()[0])
	if sub.Metadata.Name != "auth-lab-idp" || sub.Runtime.Type != "kind" || len(sub.Runtime.Kind.PreloadImages) != 1 || len(sub.Bootstrap.Manifests) != 1 {
		t.Fatalf("linked cluster config = %+v", sub)
	}
	if !sub.Runtime.Kind.Addons.SkipHostPorts {
		t.Error("a linked cluster's gateway must not take the lab's host ports")
	}
	if len(sub.KindLabs()) != 0 {
		t.Error("a linked cluster must not have linked clusters of its own")
	}

	_, states, err := kindLabStates(cfg, "astro-test-auth-lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0] != (cluster.LinkState{Name: "idp", Cluster: "astro-test-auth-lab-idp"}) || states[1].Cluster != "astro-test-auth-lab-db" {
		t.Fatalf("states = %+v — test copies must get their own cluster names", states)
	}
	if _, none, _ := kindLabStates(&config.LabConfig{}, "astro-x"); none != nil {
		t.Error("lab without linked clusters has states")
	}
}

func TestLinksConfigMap(t *testing.T) {
	data, err := linksConfigMap([]cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}})
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
	if cm.Kind != "ConfigMap" || cm.Metadata.Name != "astrona-links" || cm.Data["idp.host"] != "astro-app-idp-control-plane" || cm.Data["idp.context"] != "kind-astro-app-idp" {
		t.Fatalf("configmap = %+v", cm)
	}
}

func TestMarkLinkedClusters(t *testing.T) {
	rows := []labRow{{name: "astro-app", details: "kubectl --context kind-astro-app"}, {name: "astro-app-idp", details: "kubectl --context kind-astro-app-idp"}}
	markLinkedClusters(rows, map[string]string{"astro-app-idp": "astro-app"})
	if strings.Contains(rows[0].details, "linked") || !strings.HasPrefix(rows[1].details, "linked cluster of astro-app · kubectl") {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestKindLabStatesFollowDependencies(t *testing.T) {
	cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{Labs: []config.KindLab{
		{Name: "app", DependsOn: []string{"db"}},
		{Name: "db"},
	}}}}
	order, states, err := kindLabStates(cfg, "astro-x")
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
	env := teardownEnvironment("astro-no-such-lab-for-test", config.RuntimeConfig{Type: "kind"}, ui.Discard())
	local, ok := env.Executor.(executor.LocalExecutor)
	if !ok || local.Kubeconfig != os.DevNull {
		t.Fatalf("fallback executor = %#v — want a LocalExecutor with KUBECONFIG=%s", env.Executor, os.DevNull)
	}
}

func TestShellKubeconfigs(t *testing.T) {
	links := []cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}, {Name: "db", Cluster: "astro-app-db"}, {Name: "gone", Cluster: "astro-app-gone"}}
	kc := func(c string) string {
		if c == "astro-app-gone" {
			return ""
		}
		return "/k/" + c
	}
	if got := strings.Join(shellKubeconfigs("/k/astro-app", "astro-app", links, kc), " "); got != "/k/astro-app /k/astro-app-idp /k/astro-app-db" {
		t.Errorf("own first = %s", got)
	}
	if got := strings.Join(shellKubeconfigs("/k/astro-app", "astro-app-db", links, kc), " "); got != "/k/astro-app-db /k/astro-app /k/astro-app-idp" {
		t.Errorf("--cluster db = %s", got)
	}

	if c, err := linkedCluster("astro-app", "idp", links); err != nil || c != "astro-app-idp" {
		t.Errorf("linkedCluster = %s, %v", c, err)
	}
	if _, err := linkedCluster("astro-app", "cache", links); err == nil || !strings.Contains(err.Error(), "it has: idp, db, gone") {
		t.Errorf("unknown = %v", err)
	}
	if _, err := linkedCluster("astro-app", "idp", nil); err == nil || !strings.Contains(err.Error(), "no linked clusters") {
		t.Errorf("no links = %v", err)
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

	cfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: "ca-lab"}, Runtime: config.RuntimeConfig{Kind: &config.KindConfig{SharedCA: true, Labs: []config.KindLab{{Name: "idp"}}}}}
	if err := prepareSharedCA(cfg, "astro-ca-lab"); err != nil {
		t.Fatal(err)
	}
	sub := kindLabConfig(cfg, cfg.KindLabs()[0])
	if !sub.Runtime.Kind.SharedCA || sub.Runtime.Kind.CALab != "astro-ca-lab" {
		t.Errorf("linked cluster doesn't install the lab's CA: %+v", sub.Runtime.Kind)
	}
	if env := labCAEnv("astro-ca-lab"); len(env) != 1 || env[0] != caEnvVar+"="+ca.CertPath {
		t.Errorf("labCAEnv = %v", env)
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
