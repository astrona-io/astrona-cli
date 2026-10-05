package config

import (
	"strings"
	"testing"
)

func TestImageHasPinnedTag(t *testing.T) {
	cases := map[string]bool{
		"nginx:1.27-alpine":                       true,
		"docker.io/library/nginx:1.27":            true,
		"localhost:5000/team/app:v2":              true,
		"nginx@sha256:" + strings.Repeat("a", 64): true,
		"nginx":                   false,
		"nginx:latest":            false,
		"localhost:5000/team/app": false, // the colon is the registry port, not a tag
		"ghcr.io/org/app:latest":  false,
	}
	for ref, want := range cases {
		if got := ImageHasPinnedTag(ref); got != want {
			t.Errorf("ImageHasPinnedTag(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestValidatePreloadImages(t *testing.T) {
	ok := RuntimeConfig{Kind: &KindConfig{PreloadImages: []string{"nginx:1.27-alpine", "busybox:1.36"}}}
	if err := ValidateKindConfig(ok); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if (&KindConfig{PreloadImages: []string{"nginx:1.27"}}).IsZero() {
		t.Fatal("a kind block with only preloadImages must not count as empty")
	}

	cases := []struct {
		images  []string
		wantErr string
	}{
		{[]string{"nginx"}, "explicit tag"},
		{[]string{"nginx:latest"}, "explicit tag"},
		{[]string{"nginx:1.27", "nginx:1.27"}, "listed twice"},
		{[]string{"--help:1"}, "not a valid image reference"},
		{[]string{"nginx:1.27 busybox:1"}, "not a valid image reference"},
		{make([]string, 31), "at most 30"},
	}
	for _, c := range cases {
		err := ValidateKindConfig(RuntimeConfig{Kind: &KindConfig{PreloadImages: c.images}})
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%v: error = %v, want containing %q", c.images, err, c.wantErr)
		}
	}

	if err := ValidateKindConfig(RuntimeConfig{Type: "qemu", Kind: &KindConfig{PreloadImages: []string{"nginx:1.27"}}}); err == nil {
		t.Error("preloadImages on qemu accepted")
	}
}
