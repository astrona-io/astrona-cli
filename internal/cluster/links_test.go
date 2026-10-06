package cluster

import (
	"strings"
	"testing"
)

func TestLinkStateAndEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l := LinkState{Name: "my-idp", Cluster: "astro-idp-lab"}
	if l.Host() != "astro-idp-lab-control-plane" || l.Context() != "kind-astro-idp-lab" {
		t.Fatalf("host/context = %s %s", l.Host(), l.Context())
	}
	env := strings.Join(LinkEnv([]LinkState{l}), "\n")
	for _, want := range []string{"ASTRONA_LINK_MY_IDP_HOST=astro-idp-lab-control-plane", "ASTRONA_LINK_MY_IDP_CONTEXT=kind-astro-idp-lab"} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %s:\n%s", want, env)
		}
	}
	if strings.Contains(env, "_KUBECONFIG=") {
		t.Error("KUBECONFIG var set for a lab without a kubeconfig file")
	}

	if got, _ := ReadLinks("astro-app"); got != nil {
		t.Fatalf("no links file = %v", got)
	}
	if err := WriteLinks("astro-app", []LinkState{l}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLinks("astro-app")
	if err != nil || len(got) != 1 || got[0] != l {
		t.Fatalf("ReadLinks = %v, %v", got, err)
	}
	if err := WriteLinks("astro-app", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadLinks("astro-app"); got != nil {
		t.Fatal("empty WriteLinks should remove the file")
	}
}
