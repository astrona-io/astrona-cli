package main

import (
	"testing"

	"astrona/internal/config"
)

func TestOtherKindMemoryIncludesUnmanagedClusters(t *testing.T) {
	// Two ordinary Kind clusters, one with workers and one HA cluster.
	out := "kind|control-plane\nkind|worker\nkind|worker\n" +
		"dev|control-plane\ndev|control-plane\ndev|control-plane\ndev|external-load-balancer\n" +
		"astro-new|control-plane\nastro-new-idp|control-plane\nmalformed\n"
	got := parseOtherKindMemory(out, map[string]bool{"astro-new": true, "astro-new-idp": true})
	want := estimateLabMemory(&config.KindConfig{Nodes: config.KindNodes{Workers: 2}}) +
		estimateLabMemory(&config.KindConfig{Nodes: config.KindNodes{ControlPlanes: 3}})
	if got.clusters != 2 || got.bytes != want {
		t.Fatalf("existing clusters = %+v, want 2 clusters, %d bytes", got, want)
	}
	if r := evaluateLabMemoryWithOthers(nil, nil, got, 6*gib); r.status != checkWarn {
		t.Fatalf("expected contention warning: %+v", r)
	}
}

func TestKeyringQuota(t *testing.T) {
	for _, c := range []struct {
		name, data string
		status     checkStatus
	}{
		{"rootless default", "0: 112 112/112 112/200 5823/20000", checkWarn},
		{"rootful", "0: 112 112/112 112/1000000 5823/25000000", checkOK},
		{"keys exhausted", "0: 19000 19000/19000 19000/20000 4000/500000", checkWarn},
		{"bytes exhausted", "0: 112 112/112 112/20000 450000/500000", checkWarn},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := evaluateKeyringQuota(c.data)
			if len(got) != 1 || got[0].status != c.status {
				t.Fatalf("quota result: %+v, expected %v", got, c.status)
			}
		})
	}
	if got := evaluateKeyringQuota("malformed\n0: 1 1/1 invalid invalid\n"); len(got) != 0 {
		t.Fatalf("invalid quota parsed: %+v", got)
	}
}
