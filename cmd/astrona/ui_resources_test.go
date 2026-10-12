package main

import (
	"astrona/internal/cluster"
	"testing"
	"time"
)

func TestUIResourcesParseAndAggregateLinkedNodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	samples := parseUIContainerUsage("main|25.5%|1.5GiB / 8GiB\nworker|4.5%|512 MiB / 8 GiB\nlinked|10%|100MB / 2GB\ninvalid|NaN%|1GiB\nmissing|--|--\n")
	if len(samples) != 3 {
		t.Fatalf("samples: %+v", samples)
	}
	if samples["worker"].memory != 512*(1<<20) {
		t.Fatal("memory units wrong")
	}
	if err := cluster.WriteLinks("astro-lab", []cluster.LinkState{{Name: "idp", Cluster: "astro-linked"}}); err != nil {
		t.Fatal(err)
	}
	containers := map[string][]kindContainer{"astro-lab": {{name: "main"}, {name: "worker"}}, "astro-linked": {{name: "linked"}}}
	usage := uiRunUsage("astro-lab", time.Now().UTC().Format(time.RFC3339), containers, samples)
	if usage.Samples != 1 || usage.CPU != 40 || usage.Memory != 2*(1<<30)+100e6 {
		t.Fatalf("bad total: %+v", usage)
	}
	delete(samples, "linked")
	if got := uiRunUsage("astro-lab", time.Now().UTC().Format(time.RFC3339), containers, samples); got.Samples != 0 {
		t.Fatal("partial sample treated as complete lab")
	}
}
