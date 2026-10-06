package proctor

import (
	"strings"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/runtime"
)

func TestTargetFor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := &runtime.LabEnvironment{Type: runtime.RuntimeKind, KubeContext: "kind-astro-app", Kubeconfig: "/app.kubeconfig"}
	env.WithLinks([]cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}})
	p := NewProctor("", env)

	own, err := p.targetFor(config.ValidationCheck{})
	if err != nil || own.context != "kind-astro-app" || own.kubeconfig != "/app.kubeconfig" {
		t.Fatalf("own target = %+v, %v", own, err)
	}
	linked, err := p.targetFor(config.ValidationCheck{Cluster: "idp"})
	if err != nil || linked.context != "kind-astro-app-idp" {
		t.Fatalf("linked target = %+v, %v", linked, err)
	}
	if _, err := p.targetFor(config.ValidationCheck{Cluster: "db"}); err == nil || !strings.Contains(err.Error(), "isn't running for this lab") {
		t.Fatalf("unattached link = %v — must fail, not grade this lab", err)
	}
}
