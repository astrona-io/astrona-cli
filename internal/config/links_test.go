package config

import (
	"strings"
	"testing"
)

func TestValidateLinks(t *testing.T) {
	ok := &LabConfig{Links: []Link{{Name: "idp", Lab: "../idp-lab"}, {Name: "db", Lab: "shared-db"}}}
	if err := ValidateLinks(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]*LabConfig{
		"qemu lab":       {Runtime: RuntimeConfig{Type: "qemu"}, Links: []Link{{Name: "a", Lab: "x"}}},
		"bad name":       {Links: []Link{{Name: "Idp", Lab: "x"}}},
		"name too long":  {Links: []Link{{Name: strings.Repeat("a", 25), Lab: "x"}}},
		"duplicate":      {Links: []Link{{Name: "a", Lab: "x"}, {Name: "a", Lab: "y"}}},
		"empty lab":      {Links: []Link{{Name: "a"}}},
		"bad lab name":   {Links: []Link{{Name: "a", Lab: "two words"}}},
		"too many links": {Links: []Link{{Name: "a", Lab: "x"}, {Name: "b", Lab: "x"}, {Name: "c", Lab: "x"}, {Name: "d", Lab: "x"}, {Name: "e", Lab: "x"}, {Name: "f", Lab: "x"}}},
	}
	for name, c := range cases {
		if ValidateLinks(c) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for lab, want := range map[string]bool{"../idp": true, "./x": true, "idp/config.yaml": true, "x.yaml": true, ".": true, "idp-lab": false} {
		if got := (Link{Lab: lab}).IsPath(); got != want {
			t.Errorf("IsPath(%q) = %v", lab, got)
		}
	}
	if p := (Link{Name: "my-idp"}).EnvPrefix(); p != "ASTRONA_LINK_MY_IDP" {
		t.Errorf("EnvPrefix = %s", p)
	}
}
