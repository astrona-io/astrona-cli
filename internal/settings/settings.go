// Package settings owns the user's saved CLI preferences: their schema and
// validation, the settings.json file and its lock, the settings review model
// and the cache policy derived from the saved values. It is a foundation
// package: it never prompts and must not depend on command, UI or lab
// orchestration packages.
package settings

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

	"astrona/internal/apphome"
	"astrona/internal/localdata"
)

// MaxParallel bounds --parallel: every cluster at once is a lot of memory.
const MaxParallel = 5

// Definition describes one supported setting and its built-in default.
type Definition struct {
	Key, Default, Description string
}

// Definitions lists every supported setting in display order.
func Definitions() []Definition {
	return []Definition{
		{"verbose", "false", "Stream full command output"},
		{"keep-context", "false", "Leave your kubectl current-context unchanged"},
		{"parallel", "1", "Maximum linked clusters to start at once"},
		{"login-reminder", "true", "Ask guests to log in before starting a lab; disabling this allows guest runs without reminders"},
		{"usage-samples", "true", "While signed in, send a running lab's CPU and memory use to its Astrona lab session; no stops it"},
		{"update-check", "true", "Check for new CLI releases and show update notices"},
		{"cache-retention-days", strconv.Itoa(localdata.DefaultRetentionDays), "Maximum disposable-cache age in days (1–36500)"},
		{"cache-max-size-gb", strconv.Itoa(localdata.DefaultMaxSizeGB), "Combined disposable-cache size in GB; 0 means unlimited"},
		{"browser", `"ask"`, "Open a Kind lab in the browser workspace after `astrona run`: ask, always or never"},
	}
}

// withdrawn are settings an earlier release had that changed only how the
// CLI looked. A saved value is ignored and dropped on the next write, and a
// settings review does not ask about them: nothing about a run depends on
// them. ui-task-width sized the terminal UI's task pane, which is gone.
var withdrawn = map[string]bool{"ui-task-width": true}

// Browser preferences: the saved answer to "Open this lab in your browser?".
const (
	BrowserAsk    = "ask"
	BrowserAlways = "always"
	BrowserNever  = "never"
)

// Browser returns the saved browser preference (BrowserAsk by default).
func (s Values) Browser() string {
	value, err := s.Value("browser")
	if err != nil {
		return BrowserAsk
	}
	var text string
	if json.Unmarshal(value, &text) != nil {
		return BrowserAsk
	}
	return text
}

// Values contains only saved overrides; omitted keys use CLI defaults.
type Values map[string]json.RawMessage

// Path returns the settings file: file when set (--settings-file), otherwise
// settings.json in the active profile's home directory. It is computed on
// each call so it follows apphome.Configure.
func Path(file string) (string, error) {
	if file != "" {
		return file, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find your settings directory: %w", err)
	}
	return filepath.Join(home, apphome.Name(), "settings.json"), nil
}

// ValidateParallel checks --parallel.
func ValidateParallel(n int) error {
	if n < 1 || n > MaxParallel {
		return fmt.Errorf("--parallel must be between 1 and %d", MaxParallel)
	}
	return nil
}

// Parse validates value for key and returns its normalized JSON form.
func Parse(key, value string) (json.RawMessage, error) {
	switch key {
	case "verbose", "keep-context", "update-check", "login-reminder", "usage-samples":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "yes", "y", "true", "on":
			value = "true"
		case "no", "n", "false", "off":
			value = "false"
		default:
			return nil, fmt.Errorf("%s must be yes or no", key)
		}
	case "parallel":
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("parallel must be a whole number")
		}
		if err := ValidateParallel(n); err != nil {
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
	case "browser":
		// Stored as a JSON string. Accept the typed word or its stored form.
		text := strings.ToLower(strings.TrimSpace(value))
		if unquoted, err := strconv.Unquote(text); err == nil {
			text = unquoted
		}
		switch text {
		case BrowserAsk, BrowserAlways, BrowserNever:
			value = strconv.Quote(text)
		default:
			return nil, fmt.Errorf("browser must be ask, always or never")
		}
	default:
		return nil, fmt.Errorf("unknown setting %q; run `astrona settings` to list supported settings", key)
	}
	return json.RawMessage(value), nil
}

// UsageSamples reports whether the settings in path allow sending a running
// lab's CPU and memory samples (and its usage summary) to its lab session.
// A settings file that cannot be read counts as no: when in doubt, send nothing.
func UsageSamples(path string) bool {
	saved, err := Load(path)
	if err != nil {
		return false
	}
	value, err := saved.Value("usage-samples")
	return err == nil && string(value) == "true"
}

// ValueText shows a stored or typed value the way users answer: yes/no for
// booleans, words without their JSON quotes, anything else unchanged.
func ValueText(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "y":
		return "yes"
	case "false", "no", "n":
		return "no"
	default:
		if strings.HasPrefix(value, `"`) {
			if text, err := strconv.Unquote(value); err == nil {
				return text
			}
		}
		return value
	}
}

// LoadCachePolicy reads the disposable-cache limits saved in path.
func LoadCachePolicy(path string) (localdata.CachePolicy, error) {
	saved, err := Load(path)
	if err != nil {
		return localdata.CachePolicy{}, err
	}
	age, _ := saved.Value("cache-retention-days") // known keys with validated integers
	size, _ := saved.Value("cache-max-size-gb")
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

// Value returns the saved value for key, or its default when none is saved.
func (s Values) Value(key string) (json.RawMessage, error) {
	for _, def := range Definitions() {
		if def.Key == key {
			if value, ok := s[key]; ok {
				return value, nil
			}
			return json.RawMessage(def.Default), nil
		}
	}
	return nil, fmt.Errorf("unknown setting %q; run `astrona settings` to list supported settings", key)
}

// Load never creates files and refuses malformed settings rather than
// silently changing command behavior or replacing a file that needs repair.
func Load(path string) (Values, error) {
	return loadWithRetired(path, nil)
}

func loadWithRetired(path string, retired map[string]bool) (Values, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return Values{}, nil
	}
	var saved Values
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("invalid settings in %s; edit the file to repair it: %w", path, err)
	}
	if saved == nil {
		return nil, fmt.Errorf("settings %s must contain a JSON object", path)
	}
	for key, value := range saved {
		if withdrawn[key] {
			delete(saved, key)
			continue
		}
		if retired[key] {
			continue
		}
		parsed, err := Parse(key, strings.TrimSpace(string(value)))
		if err != nil {
			return nil, fmt.Errorf("invalid settings in %s; edit the file to repair it: %w", path, err)
		}
		saved[key] = parsed
	}
	return saved, nil
}

func readFile(path string) ([]byte, error) {
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

// Change serializes CLI writers with a file lock, then atomically replaces
// settings.json with a private file. A nil value removes an override.
func Change(path, key string, value *string) error {
	if _, err := (Values{}).Value(key); err != nil {
		return err
	}
	var parsed json.RawMessage
	if value != nil {
		var err error
		parsed, err = Parse(key, *value)
		if err != nil {
			return err
		}
	}
	return WithLock(path, func() error {
		saved, err := Load(path)
		if err != nil {
			return err
		}
		if value == nil {
			delete(saved, key)
		} else {
			saved[key] = parsed
		}
		return WriteFile(path, saved)
	})
}

// WithLock runs change while holding path+".lock", creating the settings
// directory if needed. It fails at once when another writer holds the lock.
func WithLock(path string, change func() error) error {
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

// WriteFile atomically replaces path with value as indented JSON. Callers
// hold settings.json.lock while replacing preferences or their review.
func WriteFile(path string, value any) error {
	if _, err := readFile(path); err != nil {
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

// Reset removes every saved override under the settings lock, so each
// setting falls back to its default. It works on a file Load refuses
// (invalid JSON, an unknown key): resetting is how such a file is repaired. It
// returns the values that were saved (nil when the file was unreadable as
// settings). Anything but a regular file is left alone. Unless apply is set it
// only reports. settings-review.json is kept: resetting values does not ask
// the review questions again.
func Reset(path string, apply bool) (saved map[string]json.RawMessage, damaged, existed bool, err error) {
	change := func() error {
		data, err := readFile(path)
		if err != nil {
			return err
		}
		if data == nil {
			return nil
		}
		existed = true
		if err := json.Unmarshal(data, &saved); err != nil || saved == nil {
			saved, damaged = nil, true
		}
		if !apply {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove settings %s: %w", path, err)
		}
		return nil
	}
	if !apply {
		err = change() // a preview takes no lock and writes nothing
		return saved, damaged, existed, err
	}
	err = WithLock(path, change)
	return saved, damaged, existed, err
}
