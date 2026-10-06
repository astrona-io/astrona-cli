package main

import (
	"strings"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/lifecycle"
)

func TestMarkLinkedClusters(t *testing.T) {
	rows := []labRow{{name: "astro-app", details: "kubectl --context kind-astro-app"}, {name: "astro-app-idp", details: "kubectl --context kind-astro-app-idp"}}
	markLinkedClusters(rows, map[string]string{"astro-app-idp": "astro-app"})
	if strings.Contains(rows[0].details, "linked") || !strings.HasPrefix(rows[1].details, "linked cluster of astro-app · kubectl") {
		t.Fatalf("rows = %+v", rows)
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

	if c, err := lifecycle.FindLinkedCluster("astro-app", "idp", links); err != nil || c != "astro-app-idp" {
		t.Errorf("lifecycle.FindLinkedCluster = %s, %v", c, err)
	}
	if _, err := lifecycle.FindLinkedCluster("astro-app", "cache", links); err == nil || !strings.Contains(err.Error(), "it has: idp, db, gone") {
		t.Errorf("unknown = %v", err)
	}
	if _, err := lifecycle.FindLinkedCluster("astro-app", "idp", nil); err == nil || !strings.Contains(err.Error(), "no linked clusters") {
		t.Errorf("no links = %v", err)
	}
}
