package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// validateOpenURL checks a URL handed to `astrona run --open` before anything
// is started: it is pasted from a web page, so only an absolute http(s) URL
// with a host and no embedded credentials is accepted.
func validateOpenURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("--open: not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("--open: only http and https URLs can be opened, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("--open: the URL has no host")
	}
	if u.User != nil {
		return nil, fmt.Errorf("--open: the URL must not contain a username or password")
	}
	return u, nil
}

// browserCommand is the command that opens rawURL in the default browser on
// this machine, or nil when there is no browser to open (a headless Linux box).
// The URL is passed as a single argument — never through a shell.
func browserCommand(goos string, getenv func(string) string, lookPath func(string) (string, error), rawURL string) []string {
	has := func(name string) bool { _, err := lookPath(name); return err == nil }
	switch goos {
	case "darwin":
		return []string{"open", rawURL}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", rawURL}
	case "linux":
		// Under WSL (how astrona runs on Windows) the browser is Windows'.
		if getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != "" {
			if has("wslview") {
				return []string{"wslview", rawURL}
			}
			if has("rundll32.exe") {
				return []string{"rundll32.exe", "url.dll,FileProtocolHandler", rawURL}
			}
			return nil
		}
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return nil
		}
		if has("xdg-open") {
			return []string{"xdg-open", rawURL}
		}
	}
	return nil
}

// openInBrowser opens rawURL in the default browser without waiting for it.
// It reports false when there is no browser here or it could not be started;
// the caller has printed the URL either way.
func openInBrowser(rawURL string) bool {
	argv := browserCommand(runtime.GOOS, os.Getenv, exec.LookPath, rawURL)
	if argv == nil {
		return false
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Process.Release()
	return true
}

// printAndOpen prints the lab page URL and opens it in the browser.
func printAndOpen(u *url.URL) {
	fmt.Printf("\nLab page: %s\n", u)
	if openInBrowser(u.String()) {
		fmt.Println("Opened it in your browser — the clock starts there.")
		return
	}
	fmt.Println("Open it in your browser to start the clock.")
}
