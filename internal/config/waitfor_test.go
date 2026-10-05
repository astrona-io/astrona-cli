package config

import (
	"strings"
	"testing"
	"time"
)

func TestWaitForValidate(t *testing.T) {
	cases := []struct {
		name    string
		w       WaitFor
		wantErr string
	}{
		{"named deployment", WaitFor{Resource: "deploy/web"}, ""},
		{"pods by selector", WaitFor{Resource: "pod", Selector: "app=web,tier in (fe,be)"}, ""},
		{"all nodes", WaitFor{Resource: "nodes", All: true}, ""},
		{"crd by name", WaitFor{Resource: "crd/certificates.cert-manager.io"}, ""},
		{"custom kind with condition", WaitFor{Resource: "certificates.cert-manager.io/web-tls", Namespace: "web", Condition: "Ready", Timeout: "5m"}, ""},
		{"rollout by selector", WaitFor{Resource: "deployment", Selector: "app=web"}, ""},
		{"job default Complete", WaitFor{Resource: "job/seed"}, ""},

		{"empty resource", WaitFor{}, "must be '<kind>/<name>'"},
		{"no target", WaitFor{Resource: "pod"}, "exactly one of"},
		{"name and selector", WaitFor{Resource: "pod/x", Selector: "app=x"}, "exactly one of"},
		{"selector and all", WaitFor{Resource: "pod", Selector: "app=x", All: true}, "exactly one of"},
		{"bad object name", WaitFor{Resource: "deploy/Web_1"}, "not a valid Kubernetes object name"},
		{"nested name", WaitFor{Resource: "deploy/a/b"}, "not a valid Kubernetes object name"},
		{"flag in kind", WaitFor{Resource: "--all-namespaces", All: true}, "must be '<kind>/<name>'"},
		{"selector injection", WaitFor{Resource: "pod", Selector: "app=x; rm -rf ~"}, "not a valid label selector"},
		{"selector leading dash", WaitFor{Resource: "pod", Selector: "-A"}, "not a valid label selector"},
		{"bad namespace", WaitFor{Resource: "pod/x", Namespace: "Kube_System"}, "namespace"},
		{"unknown kind without condition", WaitFor{Resource: "service/web"}, "no default condition"},
		{"rollout on pod", WaitFor{Resource: "pod/x", Condition: "rollout"}, "only works for deployments"},
		{"rollout with all", WaitFor{Resource: "deploy", All: true}, "not all: true"},
		{"lowercase condition", WaitFor{Resource: "pod/x", Condition: "ready"}, "status condition"},
		{"condition with jsonpath", WaitFor{Resource: "pod/x", Condition: "jsonpath={.x}"}, "status condition"},
		{"bad timeout", WaitFor{Resource: "pod/x", Timeout: "soon"}, "not a duration"},
		{"timeout too long", WaitFor{Resource: "pod/x", Timeout: "2h"}, "between 1s"},
		{"negative timeout", WaitFor{Resource: "pod/x", Timeout: "-1s"}, "between 1s"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.w.Validate("bootstrap")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

func TestWaitForDefaults(t *testing.T) {
	cases := map[string]string{
		"deploy/web": ConditionRollout,
		"sts/db":     ConditionRollout,
		"pod/x":      "Ready",
		"NODE/x":     "Ready",
		"job/seed":   "Complete",
		"crd/x.y":    "Established",
	}
	for res, want := range cases {
		if got := (WaitFor{Resource: res}).EffectiveCondition(); got != want {
			t.Errorf("EffectiveCondition(%s) = %q, want %q", res, got, want)
		}
	}
	if got := (WaitFor{Resource: "pod/x", Condition: "Initialized"}).EffectiveCondition(); got != "Initialized" {
		t.Errorf("explicit condition overridden: %q", got)
	}

	w := WaitFor{Resource: "pod/x"}
	if w.EffectiveNamespace() != "default" || w.EffectiveTimeout() != DefaultWaitTimeout {
		t.Errorf("defaults: ns=%q timeout=%s", w.EffectiveNamespace(), w.EffectiveTimeout())
	}
	if got := (WaitFor{Resource: "pod/x", Timeout: "90s"}).EffectiveTimeout(); got != 90*time.Second {
		t.Errorf("EffectiveTimeout = %s", got)
	}

	labels := map[string]WaitFor{
		"web ready":      {Name: "web ready", Resource: "deploy/web"},
		"deploy/web":     {Resource: "deploy/web"},
		"pod -l app=web": {Resource: "pod", Selector: "app=web"},
		"nodes (all)":    {Resource: "nodes", All: true},
	}
	for want, w := range labels {
		if got := w.Label(); got != want {
			t.Errorf("Label = %q, want %q", got, want)
		}
	}
}

func TestValidateWaitFor(t *testing.T) {
	ok := []WaitFor{{Resource: "deploy/web"}}

	if err := ValidateWaitFor(&LabConfig{Bootstrap: BootstrapConfig{WaitFor: ok}, Testing: BootstrapConfig{WaitFor: ok}}); err != nil {
		t.Fatalf("kind lab: unexpected error: %v", err)
	}

	err := ValidateWaitFor(&LabConfig{Testing: BootstrapConfig{WaitFor: []WaitFor{{Resource: "pod"}}}})
	if err == nil || !strings.Contains(err.Error(), "testing.waitFor") {
		t.Fatalf("invalid testing entry: error = %v", err)
	}

	err = ValidateWaitFor(&LabConfig{Runtime: RuntimeConfig{Type: "qemu"}, Bootstrap: BootstrapConfig{WaitFor: ok}})
	if err == nil || !strings.Contains(err.Error(), "kubectl-reachable") {
		t.Fatalf("qemu root waitFor: error = %v", err)
	}

	err = ValidateWaitFor(&LabConfig{Runtime: RuntimeConfig{Type: "qemu", QEMU: []QEMUVM{{Name: "a", Bootstrap: &BootstrapConfig{WaitFor: ok}}}}})
	if err == nil || !strings.Contains(err.Error(), "vm 'a'") {
		t.Fatalf("qemu per-VM waitFor: error = %v", err)
	}
}
