package cluster

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"astrona/internal/config"

	"gopkg.in/yaml.v3"
)

func TestBuildKindConfigNilMeansNoFile(t *testing.T) {
	for _, k := range []*config.KindConfig{nil, {}} {
		data, err := BuildKindConfig(k)
		if err != nil || data != nil {
			t.Fatalf("BuildKindConfig(%+v) = %q, %v; want nil, nil", k, data, err)
		}
	}
}

func TestBuildKindConfig(t *testing.T) {
	k := &config.KindConfig{
		Version: "v1.31.2",
		Nodes:   config.KindNodes{ControlPlanes: 1, Workers: 2},
		Networking: config.KindNetworking{
			DisableDefaultCNI: true,
			KubeProxyMode:     "ipvs",
			PodSubnet:         "192.168.0.0/16",
		},
		FeatureGates:  map[string]bool{"InPlacePodVerticalScaling": true},
		RuntimeConfig: map[string]string{"api/alpha": "false"},
	}
	data, err := BuildKindConfig(k)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("generated config is not valid YAML: %v\n%s", err, data)
	}
	if got["kind"] != "Cluster" || got["apiVersion"] != "kind.x-k8s.io/v1alpha4" {
		t.Errorf("wrong header:\n%s", data)
	}

	nodes, _ := got["nodes"].([]any)
	var roles []string
	for _, n := range nodes {
		roles = append(roles, n.(map[string]any)["role"].(string))
	}
	if want := []string{"control-plane", "worker", "worker"}; !reflect.DeepEqual(roles, want) {
		t.Errorf("node roles = %v, want %v", roles, want)
	}

	net, _ := got["networking"].(map[string]any)
	if net["disableDefaultCNI"] != true || net["kubeProxyMode"] != "ipvs" || net["podSubnet"] != "192.168.0.0/16" {
		t.Errorf("networking = %v", net)
	}
	if _, set := net["serviceSubnet"]; set {
		t.Error("unset serviceSubnet should be omitted, not written empty")
	}

	// The image goes on the command line, never into the file — and
	// nothing outside the allowlist ever appears.
	for _, forbidden := range []string{"image", "extraMounts", "extraPortMappings", "kubeadmConfigPatches"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("generated config unexpectedly contains %q:\n%s", forbidden, data)
		}
	}
}

func TestBuildKindConfigOmitsEmptyNetworking(t *testing.T) {
	data, err := BuildKindConfig(&config.KindConfig{Nodes: config.KindNodes{Workers: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "networking") {
		t.Errorf("empty networking should be omitted:\n%s", data)
	}
}

func TestKindCreateArgs(t *testing.T) {
	cases := []struct {
		configPath, image string
		want              []string
	}{
		{"", "", []string{"create", "cluster", "--name", "astro-x"}},
		{"/tmp/c.yaml", "", []string{"create", "cluster", "--name", "astro-x", "--config", "/tmp/c.yaml"}},
		{"/tmp/c.yaml", "kindest/node:v1.31.2", []string{"create", "cluster", "--name", "astro-x", "--config", "/tmp/c.yaml", "--image", "kindest/node:v1.31.2"}},
	}
	for _, c := range cases {
		if got := kindCreateArgs("astro-x", c.configPath, c.image); !reflect.DeepEqual(got, c.want) {
			t.Errorf("kindCreateArgs(%q, %q) = %v, want %v", c.configPath, c.image, got, c.want)
		}
	}
}

func TestWriteKindConfig(t *testing.T) {
	path, cleanup, err := writeKindConfig(nil)
	if err != nil || path != "" {
		t.Fatalf("nil data: path=%q err=%v", path, err)
	}
	cleanup()

	path, cleanup, err = writeKindConfig([]byte("kind: Cluster\n"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("kind config file mode %o is readable by others", perm)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("cleanup did not remove the kind config file")
	}
}
