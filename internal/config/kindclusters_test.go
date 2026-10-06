package config

import (
	"reflect"
	"strings"
	"testing"
)

func labsCfg(labs []KindCluster, checks ...ValidationCheck) *LabConfig {
	return &LabConfig{
		Runtime:    RuntimeConfig{Type: "kind", Kind: &KindConfig{Clusters: labs}},
		Validation: ValidationConfig{Checks: checks},
	}
}

func TestValidateKindClusters(t *testing.T) {
	idp := []KindCluster{{Name: "idp", PreloadImages: []string{"nginx:1.27-alpine"}}, {Name: "db"}}
	ok := labsCfg(idp,
		ValidationCheck{Name: "idp up", Type: "resourceExists", Resource: "deploy/idp", Cluster: "idp"},
		ValidationCheck{Name: "local", Type: "resourceExists", Resource: "deploy/app"})
	if err := ValidateKindClusters(ok); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKindClusters(&LabConfig{}); err != nil {
		t.Fatalf("lab without labs: %v", err)
	}

	cases := map[string]*LabConfig{
		"bad name":                      labsCfg([]KindCluster{{Name: "Idp"}}),
		"name too long":                 labsCfg([]KindCluster{{Name: strings.Repeat("a", 25)}}),
		"duplicate":                     labsCfg([]KindCluster{{Name: "a"}, {Name: "a"}}),
		"name too long for the cluster": {Metadata: MetadataConfig{Name: strings.Repeat("a", 40)}, Runtime: RuntimeConfig{Kind: &KindConfig{Clusters: []KindCluster{{Name: "identity"}}}}},
		"too many":                      labsCfg([]KindCluster{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}, {Name: "f"}}),
		"bad cluster":                   labsCfg([]KindCluster{{Name: "a", Version: "latest"}}),
		"bad waitFor":                   labsCfg([]KindCluster{{Name: "a", Bootstrap: BootstrapConfig{WaitFor: []WaitFor{{}}}}}),
		"bad testing waitFor":           labsCfg([]KindCluster{{Name: "a", Testing: BootstrapConfig{WaitFor: []WaitFor{{}}}}}),
		"unknown cluster":               labsCfg(idp, ValidationCheck{Name: "x", Type: "count", Cluster: "cache"}),
		"no labs":                       labsCfg(nil, ValidationCheck{Name: "x", Type: "count", Cluster: "idp"}),
		"http":                          labsCfg(idp, ValidationCheck{Name: "x", Type: "http", URL: "http://a", Cluster: "idp"}),
		"unknown forward cluster":       {Runtime: RuntimeConfig{Kind: &KindConfig{Clusters: idp}, PortForwards: []PortForward{{Name: "x", Cluster: "cache"}}}},
	}
	for name, c := range cases {
		if ValidateKindClusters(c) == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	if p := (KindCluster{Name: "my-idp"}).EnvPrefix(); p != "ASTRONA_CLUSTER_MY_IDP" {
		t.Errorf("EnvPrefix = %s", p)
	}
	if n := LinkedClusterName("astro-test-app", "idp"); n != "astro-test-app-idp" {
		t.Errorf("LinkedClusterName = %s", n)
	}
	if k := (KindCluster{Name: "a", Addons: KindAddons{GatewayAPI: "envoy"}}).Cluster(); !k.Addons.SkipHostPorts {
		t.Error("a linked cluster's gateway must not take the lab's host ports")
	}
}

// TestKindClusterMirrorsKindConfig catches a field added to KindConfig but not
// to KindCluster (and Cluster) — a linked cluster would silently ignore it.
func TestKindClusterMirrorsKindConfig(t *testing.T) {
	lab := reflect.TypeOf(KindCluster{})
	kc := reflect.TypeOf(KindConfig{})
	for i := 0; i < kc.NumField(); i++ {
		f := kc.Field(i)
		if f.Name == "Clusters" || f.Name == "Labs" || f.Name == "SharedCA" || f.Name == "CALab" { // lab-wide, not per cluster
			continue
		}
		lf, ok := lab.FieldByName(f.Name)
		if !ok || lf.Type != f.Type || lf.Tag != f.Tag {
			t.Errorf("KindCluster is missing KindConfig.%s (same type and yaml tag)", f.Name)
		}
	}

	// Cluster copies every one of them.
	full := KindCluster{Name: "x", Version: "v1.31.2", Image: "i", Nodes: KindNodes{Workers: 1}, Networking: KindNetworking{IPFamily: "ipv4"},
		FeatureGates: map[string]bool{"A": true}, RuntimeConfig: map[string]string{"a": "b"}, Addons: KindAddons{CertManager: true},
		PreloadImages: []string{"busybox"}}
	got := reflect.ValueOf(*full.Cluster())
	for i := 0; i < got.NumField(); i++ {
		name := kc.Field(i).Name
		if name != "Clusters" && name != "Labs" && name != "SharedCA" && name != "CALab" && got.Field(i).IsZero() {
			t.Errorf("Cluster() doesn't copy %s", name)
		}
	}
}

func TestKindClusterOrder(t *testing.T) {
	names := func(labs []KindCluster) string {
		var out []string
		for _, l := range labs {
			out = append(out, l.Name)
		}
		return strings.Join(out, ",")
	}
	labs := []KindCluster{
		{Name: "app", DependsOn: []string{"idp", "db"}},
		{Name: "idp", DependsOn: []string{"db"}},
		{Name: "cache"},
		{Name: "db"},
	}
	order, err := KindClusterOrder(labs)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(order); got != "cache,db,idp,app" {
		t.Errorf("order = %s — dependencies first, otherwise config order", got)
	}

	for name, bad := range map[string][]KindCluster{
		"cycle":     {{Name: "a", DependsOn: []string{"b"}}, {Name: "b", DependsOn: []string{"a"}}},
		"self":      {{Name: "a", DependsOn: []string{"a"}}},
		"unknown":   {{Name: "a", DependsOn: []string{"zz"}}},
		"duplicate": {{Name: "a", DependsOn: []string{"b", "b"}}, {Name: "b"}},
	} {
		if _, err := KindClusterOrder(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if ValidateKindClusters(labsCfg(bad)) == nil {
			t.Errorf("%s: ValidateKindClusters accepted", name)
		}
	}
}

func TestDeprecatedLabsStillWork(t *testing.T) {
	old := &LabConfig{Runtime: RuntimeConfig{Kind: &KindConfig{Labs: []KindCluster{{Name: "idp"}}}}}
	old.moveDeprecatedLabs()
	if len(old.KindClusters()) != 1 || old.Runtime.Kind.Labs != nil || len(old.Deprecations) != 1 {
		t.Fatalf("labs not moved to clusters: %+v, deprecations %v", old.Runtime.Kind, old.Deprecations)
	}
	if err := ValidateKindClusters(old); err != nil {
		t.Errorf("moved config invalid: %v", err)
	}

	both := &LabConfig{Runtime: RuntimeConfig{Kind: &KindConfig{Clusters: []KindCluster{{Name: "a"}}, Labs: []KindCluster{{Name: "b"}}}}}
	both.moveDeprecatedLabs()
	if err := ValidateKindClusters(both); err == nil {
		t.Error("clusters and labs both set accepted")
	}
}

func TestValidateAPIVersion(t *testing.T) {
	for v, ok := range map[string]bool{"": true, APIVersionV1: true, "astrona.io/v2": false, "v1": false} {
		if (ValidateAPIVersion(&LabConfig{APIVersion: v}) == nil) != ok {
			t.Errorf("apiVersion %q accepted = %v", v, !ok)
		}
	}
}
