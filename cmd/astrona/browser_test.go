package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateOpenURL(t *testing.T) {
	ok := []string{
		"https://astrona.io/labs/ATS000/section-010/module-01/lab-01?clock=start&t=5a50desd",
		"http://localhost:3000/labs/ATS000/section-010/module-01/lab-01?clock=start&t=abc",
	}
	for _, raw := range ok {
		if _, err := validateOpenURL(raw); err != nil {
			t.Errorf("validateOpenURL(%q) = %v, want ok", raw, err)
		}
	}
	bad := []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"/labs/ATS000",
		"https://",
		"https://user:pass@astrona.io/labs",
		"ftp://astrona.io/x",
	}
	for _, raw := range bad {
		if _, err := validateOpenURL(raw); err == nil {
			t.Errorf("validateOpenURL(%q) accepted, want an error", raw)
		}
	}
}

func TestBrowserCommand(t *testing.T) {
	const u = "https://astrona.io/labs/x?clock=start&t=1"
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	found := func(names ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, n := range names {
				if n == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	cases := []struct {
		name   string
		goos   string
		env    map[string]string
		path   []string
		expect []string
	}{
		{"macOS", "darwin", nil, nil, []string{"open", u}},
		{"Windows", "windows", nil, nil, []string{"rundll32", "url.dll,FileProtocolHandler", u}},
		{"Linux desktop", "linux", map[string]string{"DISPLAY": ":0"}, []string{"xdg-open"}, []string{"xdg-open", u}},
		{"Linux headless", "linux", nil, []string{"xdg-open"}, nil},
		{"WSL with wslview", "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, []string{"wslview"}, []string{"wslview", u}},
		{"WSL without wslview", "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, []string{"rundll32.exe"}, []string{"rundll32.exe", "url.dll,FileProtocolHandler", u}},
		{"WSL without interop", "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, nil, nil},
	}
	for _, c := range cases {
		got := browserCommand(c.goos, env(c.env), found(c.path...), u)
		if !reflect.DeepEqual(got, c.expect) {
			t.Errorf("%s: browserCommand = %v, want %v", c.name, got, c.expect)
		}
	}
}
