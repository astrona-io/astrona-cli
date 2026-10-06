package cluster

import (
	"strings"
	"testing"
)

const kindCorefile = `.:53 {
    errors
    health {
       lameduck 5s
    }
    forward . /etc/resolv.conf {
       max_concurrent 1000
    }
    reload
}
`

func TestWithLinkHosts(t *testing.T) {
	got, err := WithLinkHosts(kindCorefile, map[string]string{"idp.astrona.internal": "10.89.1.5", "db.astrona.internal": "10.89.1.7"})
	if err != nil {
		t.Fatal(err)
	}
	want := ".:53 {\n" + rewriteBegin + "\n    hosts {\n        10.89.1.7 db.astrona.internal\n        10.89.1.5 idp.astrona.internal\n        fallthrough\n    }\n" + rewriteEnd + "\n    errors\n"
	if !strings.Contains(got, want) {
		t.Errorf("missing\n%s\nin:\n%s", want, got)
	}

	// Applying again (new IPs after a restart) replaces the block.
	again, err := WithLinkHosts(got, map[string]string{"idp.astrona.internal": "10.89.1.9"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(again, rewriteBegin) != 1 || strings.Contains(again, "10.89.1.5") || !strings.Contains(again, "10.89.1.9 idp.astrona.internal") {
		t.Errorf("re-apply didn't replace the block:\n%s", again)
	}

	if _, err := WithLinkHosts("example.org:53 {\n}\n", nil); err == nil {
		t.Error("Corefile without .:53 accepted")
	}
	if h := (LinkState{Name: "idp"}).Hostname(); h != "idp.astrona.internal" {
		t.Errorf("Hostname = %s", h)
	}
}
