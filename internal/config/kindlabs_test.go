package config

import (
	"reflect"
	"strings"
	"testing"
)

func labsCfg(labs []KindLab, checks ...ValidationCheck) *LabConfig {
	return &LabConfig{
		Runtime:    RuntimeConfig{Type: "kind", Kind: &KindConfig{Labs: labs}},
		Validation: ValidationConfig{Checks: checks},
	}
}

func TestValidateKindLabs(t *testing.T) {
	idp := []KindLab{{Name: "idp", PreloadImages: []string{"nginx:1.27-alpine"}}, {Name: "db"}}
	ok := labsCfg(idp,
		ValidationCheck{Name: "idp up", Type: "resourceExists", Resource: "deploy/idp", Cluster: "idp"},
		ValidationCheck{Name: "local", Type: "resourceExists", Resource: "deploy/app"})
	if err := ValidateKindLabs(ok); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKindLabs(&LabConfig{}); err != nil {
		t.Fatalf("lab without labs: %v", err)
	}

	cases := map[string]*LabConfig{
		"bad name":                      labsCfg([]KindLab{{Name: "Idp"}}),
		"name too long":                 labsCfg([]KindLab{{Name: strings.Repeat("a", 25)}}),
		"duplicate":                     labsCfg([]KindLab{{Name: "a"}, {Name: "a"}}),
		"name too long for the cluster": {Metadata: MetadataConfig{Name: strings.Repeat("a", 40)}, Runtime: RuntimeConfig{Kind: &KindConfig{Labs: []KindLab{{Name: "identity"}}}}},
		"too many":                      labsCfg([]KindLab{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}, {Name: "f"}}),
		"bad cluster":                   labsCfg([]KindLab{{Name: "a", Version: "latest"}}),
		"bad waitFor":                   labsCfg([]KindLab{{Name: "a", Bootstrap: BootstrapConfig{WaitFor: []WaitFor{{}}}}}),
		"bad testing waitFor":           labsCfg([]KindLab{{Name: "a", Testing: BootstrapConfig{WaitFor: []WaitFor{{}}}}}),
		"unknown cluster":               labsCfg(idp, ValidationCheck{Name: "x", Type: "count", Cluster: "cache"}),
		"no labs":                       labsCfg(nil, ValidationCheck{Name: "x", Type: "count", Cluster: "idp"}),
		"http":                          labsCfg(idp, ValidationCheck{Name: "x", Type: "http", URL: "http://a", Cluster: "idp"}),
	}
	for name, c := range cases {
		if ValidateKindLabs(c) == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	if p := (KindLab{Name: "my-idp"}).EnvPrefix(); p != "ASTRONA_LINK_MY_IDP" {
		t.Errorf("EnvPrefix = %s", p)
	}
	if n := KindLabClusterName("astro-test-app", "idp"); n != "astro-test-app-idp" {
		t.Errorf("KindLabClusterName = %s", n)
	}
	if k := (KindLab{Name: "a", Addons: KindAddons{GatewayAPI: "envoy"}}).Cluster(); !k.Addons.SkipHostPorts {
		t.Error("a linked cluster's gateway must not take the lab's host ports")
	}
}

// TestKindLabMirrorsKindConfig catches a field added to KindConfig but not
// to KindLab (and Cluster) — a linked cluster would silently ignore it.
func TestKindLabMirrorsKindConfig(t *testing.T) {
	lab := reflect.TypeOf(KindLab{})
	kc := reflect.TypeOf(KindConfig{})
	for i := 0; i < kc.NumField(); i++ {
		f := kc.Field(i)
		if f.Name == "Labs" {
			continue
		}
		lf, ok := lab.FieldByName(f.Name)
		if !ok || lf.Type != f.Type || lf.Tag != f.Tag {
			t.Errorf("KindLab is missing KindConfig.%s (same type and yaml tag)", f.Name)
		}
	}

	// Cluster copies every one of them.
	full := KindLab{Name: "x", Version: "v1.31.2", Image: "i", Nodes: KindNodes{Workers: 1}, Networking: KindNetworking{IPFamily: "ipv4"},
		FeatureGates: map[string]bool{"A": true}, RuntimeConfig: map[string]string{"a": "b"}, Addons: KindAddons{CertManager: true},
		PreloadImages: []string{"busybox"}}
	got := reflect.ValueOf(*full.Cluster())
	for i := 0; i < got.NumField(); i++ {
		name := kc.Field(i).Name
		if name != "Labs" && got.Field(i).IsZero() {
			t.Errorf("Cluster() doesn't copy %s", name)
		}
	}
}

func TestKindLabOrder(t *testing.T) {
	names := func(labs []KindLab) string {
		var out []string
		for _, l := range labs {
			out = append(out, l.Name)
		}
		return strings.Join(out, ",")
	}
	labs := []KindLab{
		{Name: "app", DependsOn: []string{"idp", "db"}},
		{Name: "idp", DependsOn: []string{"db"}},
		{Name: "cache"},
		{Name: "db"},
	}
	order, err := KindLabOrder(labs)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(order); got != "cache,db,idp,app" {
		t.Errorf("order = %s — dependencies first, otherwise config order", got)
	}

	for name, bad := range map[string][]KindLab{
		"cycle":     {{Name: "a", DependsOn: []string{"b"}}, {Name: "b", DependsOn: []string{"a"}}},
		"self":      {{Name: "a", DependsOn: []string{"a"}}},
		"unknown":   {{Name: "a", DependsOn: []string{"zz"}}},
		"duplicate": {{Name: "a", DependsOn: []string{"b", "b"}}, {Name: "b"}},
	} {
		if _, err := KindLabOrder(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if ValidateKindLabs(labsCfg(bad)) == nil {
			t.Errorf("%s: ValidateKindLabs accepted", name)
		}
	}
}
