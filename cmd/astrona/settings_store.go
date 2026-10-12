package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type settingDefinition struct {
	key, defaultValue, description string
}

func settingsDefinitions() []settingDefinition {
	return []settingDefinition{
		{"verbose", "false", "Stream full command output"},
		{"keep-context", "false", "Leave your kubectl current-context unchanged"},
		{"parallel", "1", "Maximum linked clusters to start at once"},
		{"update-check", "true", "Check for new CLI releases and show update notices"},
	}
}

// userSettings contains only saved overrides; omitted keys use CLI defaults.
type userSettings map[string]json.RawMessage

func settingsPath(flags *rootFlags) (string, error) {
	if flags.settingsFile != "" {
		return flags.settingsFile, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find your settings directory: %w", err)
	}
	return filepath.Join(home, ".astrona", "settings.json"), nil
}

func parseSetting(key, value string) (json.RawMessage, error) {
	switch key {
	case "verbose", "keep-context", "update-check":
		if value != "true" && value != "false" {
			return nil, fmt.Errorf("%s must be true or false", key)
		}
	case "parallel":
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("parallel must be a whole number")
		}
		if err := validateParallel(n); err != nil {
			return nil, err
		}
		value = strconv.Itoa(n)
	default:
		return nil, fmt.Errorf("unknown setting %q; run `astrona settings` to list supported settings", key)
	}
	return json.RawMessage(value), nil
}

func (s userSettings) value(key string) (json.RawMessage, error) {
	for _, def := range settingsDefinitions() {
		if def.key == key {
			if value, ok := s[key]; ok {
				return value, nil
			}
			return json.RawMessage(def.defaultValue), nil
		}
	}
	return nil, fmt.Errorf("unknown setting %q; run `astrona settings` to list supported settings", key)
}

// loadSettings never creates files and refuses malformed settings rather than
// silently changing command behavior or replacing a file that needs repair.
func loadSettings(path string) (userSettings, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return userSettings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("settings %s must be a regular file, not a directory or symlink", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open settings %s: %w", path, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("settings %s changed while being read; try again", path)
	}
	const maxSettingsSize = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(f, maxSettingsSize+1))
	if err != nil {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	if len(data) > maxSettingsSize {
		return nil, fmt.Errorf("settings %s exceeds 64 KiB", path)
	}
	var saved userSettings
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("invalid settings in %s; edit the file to repair it: %w", path, err)
	}
	if saved == nil {
		return nil, fmt.Errorf("settings %s must contain a JSON object", path)
	}
	for key, value := range saved {
		parsed, err := parseSetting(key, strings.TrimSpace(string(value)))
		if err != nil {
			return nil, fmt.Errorf("invalid settings in %s; edit the file to repair it: %w", path, err)
		}
		saved[key] = parsed
	}
	return saved, nil
}

// changeSetting serializes CLI writers with a file lock, then atomically
// replaces settings.json with a private file. A nil value removes an override.
func changeSetting(path, key string, value *string) error {
	if _, err := (userSettings{}).value(key); err != nil {
		return err
	}
	var parsed json.RawMessage
	if value != nil {
		var err error
		parsed, err = parseSetting(key, *value)
		if err != nil {
			return err
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	// The lock file stays in place; closing its descriptor releases the lock,
	// including after a crash. Never follow a symlink for this writable file.
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open settings lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("lock settings (another command may be changing them; try again): %w", err)
	}
	saved, err := loadSettings(path)
	if err != nil {
		return err
	}
	if value == nil {
		delete(saved, key)
	} else {
		saved[key] = parsed
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp") // created with mode 0600
	if err != nil {
		return fmt.Errorf("create settings file: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close settings: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
