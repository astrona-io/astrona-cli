package main

import "testing"

func TestIsHomebrewInstall(t *testing.T) {
	for _, c := range []struct {
		exe  string
		want bool
	}{
		{"/opt/homebrew/Cellar/astrona/0.3.0/bin/astrona", true},
		{"/usr/local/Cellar/astrona/0.3.0/bin/astrona", true},
		{"/home/linuxbrew/.linuxbrew/Cellar/astrona/0.3.0/bin/astrona", true},
		{"/Users/me/.local/bin/astrona", false},
		{"/usr/local/bin/astrona", false},
		{"/Users/me/.astrona/bin/astrona-0.2.1", false},
		{"/opt/homebrew/Cellar/kind/0.24.0/bin/kind", false},
	} {
		if got := isHomebrewInstall(c.exe); got != c.want {
			t.Errorf("isHomebrewInstall(%q) = %v, want %v", c.exe, got, c.want)
		}
	}
}
