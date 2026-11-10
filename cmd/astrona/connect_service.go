package main

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/logs"
)

// The service `astrona connect --install` sets up. A development build gets
// its own, so it never replaces the release's.
func connectServiceName() string {
	if apphome.Development() {
		return "astrona-connect-dev"
	}
	return "astrona-connect"
}

func connectLaunchLabel() string {
	return "io.astrona." + strings.TrimPrefix(connectServiceName(), "astrona-")
}

// connectServiceEnv is what the service needs from this shell: the PATH that
// finds docker/podman and kind, and the engine and site choices.
func connectServiceEnv() [][2]string {
	var env [][2]string
	for _, k := range []string{"PATH", "KIND_EXPERIMENTAL_PROVIDER", "DOCKER_HOST", "CONTAINER_HOST", "ASTRONA_URL"} {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			env = append(env, [2]string{k, v})
		}
	}
	return env
}

// connectExecutable is the astrona the service runs: the one on PATH when
// it is this same binary (a package manager's link keeps working across
// upgrades), otherwise this binary's own path.
func connectExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	if onPath, err := exec.LookPath(filepath.Base(exe)); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil {
			if r, err := filepath.EvalSymlinks(abs); err == nil && r == resolved {
				return abs, nil
			}
		}
	}
	return resolved, nil
}

func connectLogPath() (string, error) {
	dir, err := logs.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connect.log"), nil
}

func installConnectService() error {
	exe, err := connectExecutable()
	if err != nil {
		return err
	}
	logPath, err := connectLogPath()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(exe, logPath)
	case "linux":
		return installSystemdUnit(exe, logPath)
	default:
		return fmt.Errorf("--install isn't supported on %s yet; keep astrona connect running in a terminal", runtime.GOOS)
	}
}

func uninstallConnectService() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchAgent()
	case "linux":
		return uninstallSystemdUnit()
	default:
		return fmt.Errorf("--uninstall isn't supported on %s", runtime.GOOS)
	}
}

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", connectLaunchLabel()+".plist"), nil
}

func launchAgentPlist(exe, logPath string) string {
	x := html.EscapeString
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + x(connectLaunchLabel()) + `</string>
  <key>ProgramArguments</key>
  <array><string>` + x(exe) + `</string><string>connect</string><string>--service</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>ExitTimeOut</key><integer>150</integer>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>` + x(logPath) + `</string>
  <key>StandardErrorPath</key><string>` + x(logPath) + `</string>
  <key>EnvironmentVariables</key>
  <dict>
`)
	for _, kv := range connectServiceEnv() {
		b.WriteString("    <key>" + x(kv[0]) + "</key><string>" + x(kv[1]) + "</string>\n")
	}
	b.WriteString("  </dict>\n</dict>\n</plist>\n")
	return b.String()
}

func launchDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func installLaunchAgent(exe, logPath string) error {
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Replacing an installed agent: unload the old one first (fine if none).
	_ = exec.Command("launchctl", "bootout", launchDomain()+"/"+connectLaunchLabel()).Run()
	if err := os.WriteFile(path, []byte(launchAgentPlist(exe, logPath)), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("launchctl", "bootstrap", launchDomain(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("astrona connect now starts when you log in, and is running.\n  Agent: %s\n  Log:   %s\nRemove it with: astrona connect --uninstall\n", path, logPath)
	return nil
}

func uninstallLaunchAgent() error {
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", launchDomain()+"/"+connectLaunchLabel()).Run()
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		fmt.Println("astrona connect wasn't installed.")
		return nil
	} else if err != nil {
		return err
	}
	fmt.Println("astrona connect no longer starts when you log in, and has stopped.")
	return nil
}

func systemdUnitPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "systemd", "user", connectServiceName()+".service"), nil
}

// systemdQuote quotes one word of a unit file's command line or Environment=.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + r.Replace(s) + `"`
}

func systemdUnit(exe, logPath string) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=astrona connect: lets the Astrona website start labs on this computer\n" +
		"After=network-online.target\n\n[Service]\n")
	b.WriteString("ExecStart=" + systemdQuote(exe) + " connect --service\n")
	for _, kv := range connectServiceEnv() {
		b.WriteString("Environment=" + systemdQuote(kv[0]+"="+kv[1]) + "\n")
	}
	b.WriteString("Restart=on-failure\nRestartSec=10\n")
	// Stopping signals connect alone, which interrupts a running lab command
	// once and waits for it to clean up (up to two minutes).
	b.WriteString("KillMode=mixed\nTimeoutStopSec=150\n")
	b.WriteString("StandardOutput=append:" + logPath + "\nStandardError=append:" + logPath + "\n\n")
	b.WriteString("[Install]\nWantedBy=default.target\n")
	return b.String()
}

func systemctlUser(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func installSystemdUnit(exe, logPath string) error {
	path, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(systemdUnit(exe, logPath)), 0o644); err != nil {
		return err
	}
	if err := systemctlUser("daemon-reload"); err != nil {
		return err
	}
	if err := systemctlUser("enable", "--now", connectServiceName()+".service"); err != nil {
		return err
	}
	// Restart picks up a changed unit when it was already running.
	if err := systemctlUser("restart", connectServiceName()+".service"); err != nil {
		return err
	}
	fmt.Printf("astrona connect now starts when you log in, and is running.\n  Unit: %s\n  Log:  %s\n"+
		"It runs while you are logged in; `loginctl enable-linger` keeps it running after you log out.\n"+
		"Remove it with: astrona connect --uninstall\n", path, logPath)
	return nil
}

func uninstallSystemdUnit() error {
	path, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Println("astrona connect wasn't installed.")
		return nil
	}
	_ = systemctlUser("disable", "--now", connectServiceName()+".service")
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = systemctlUser("daemon-reload")
	fmt.Println("astrona connect no longer starts when you log in, and has stopped.")
	return nil
}
