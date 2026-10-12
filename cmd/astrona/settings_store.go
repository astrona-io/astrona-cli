package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"astrona/internal/localdata"
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
		{"cache-retention-days", strconv.Itoa(localdata.DefaultRetentionDays), "Maximum disposable-cache age in days (1–36500)"},
		{"cache-max-size-gb", strconv.Itoa(localdata.DefaultMaxSizeGB), "Combined disposable-cache size in GB; 0 means unlimited"},
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
	case "cache-retention-days", "cache-max-size-gb":
		n, err := strconv.ParseInt(value, 10, 64)
		minValue, maxValue := int64(0), int64(math.MaxInt64/localdata.BytesPerGB)
		if key == "cache-retention-days" {
			minValue, maxValue = 1, 36500
		}
		if err != nil || n < minValue || n > maxValue {
			return nil, fmt.Errorf("%s must be a whole number between %d and %d", key, minValue, maxValue)
		}
		value = strconv.FormatInt(n, 10)
	default:
		return nil, fmt.Errorf("unknown setting %q; run `astrona settings` to list supported settings", key)
	}
	return json.RawMessage(value), nil
}

func loadCachePolicy(path string) (localdata.CachePolicy, error) {
	saved, err := loadSettings(path)
	if err != nil {
		return localdata.CachePolicy{}, err
	}
	age, _ := saved.value("cache-retention-days") // known keys with validated integers
	size, _ := saved.value("cache-max-size-gb")
	days, err := strconv.ParseInt(string(age), 10, 64)
	if err != nil {
		return localdata.CachePolicy{}, err
	}
	gb, err := strconv.ParseInt(string(size), 10, 64)
	if err != nil {
		return localdata.CachePolicy{}, err
	}
	return localdata.CachePolicy{MaxAge: time.Duration(days) * 24 * time.Hour, MaxBytes: gb * localdata.BytesPerGB}, nil
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
	return loadSettingsWithRetired(path, nil)
}

func loadSettingsWithRetired(path string, retired map[string]bool) (userSettings, error) {
	data, err := readSettingsFile(path)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return userSettings{}, nil
	}
	var saved userSettings
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("invalid settings in %s; edit the file to repair it: %w", path, err)
	}
	if saved == nil {
		return nil, fmt.Errorf("settings %s must contain a JSON object", path)
	}
	for key, value := range saved {
		if retired[key] {
			continue
		}
		parsed, err := parseSetting(key, strings.TrimSpace(string(value)))
		if err != nil {
			return nil, fmt.Errorf("invalid settings in %s; edit the file to repair it: %w", path, err)
		}
		saved[key] = parsed
	}
	return saved, nil
}

func readSettingsFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
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
	return data, nil
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
	return withSettingsLock(path, func() error {
		saved, err := loadSettings(path)
		if err != nil {
			return err
		}
		if value == nil {
			delete(saved, key)
		} else {
			saved[key] = parsed
		}
		return writeSettingsFile(path, saved)
	})
}

func withSettingsLock(path string, change func() error) error {
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
	return change()
}

// Callers hold settings.json.lock while replacing preferences or their review.
func writeSettingsFile(path string, value any) error {
	if _, err := readSettingsFile(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp") // created with mode 0600
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
