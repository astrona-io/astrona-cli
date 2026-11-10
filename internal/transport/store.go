package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// maxKeys is how many keys the file keeps: the one in use, and a new one
// from a login that has not finished saving its sign-in yet.
const maxKeys = 3

// maxFileSize caps how much of the key file is read.
const maxFileSize = 64 << 10

// KeyFile is ~/.astrona/device-key.json: the private halves of this
// computer's device keys, 0600. Callers serialize writes with the sign-in
// lock (account.Store.Lock).
type KeyFile struct {
	Path string
}

type savedKey struct {
	ID          string    `json:"key_id"`
	SigningSeed string    `json:"signing_seed"`
	DHPrivate   string    `json:"dh_private"`
	CreatedAt   time.Time `json:"created_at"`
}

type keyFileData struct {
	Keys []savedKey `json:"keys"`
}

// Load returns the key whose fingerprint is id; (nil, nil) when the file
// or that key is not there. Like the credentials file, it refuses a file
// other users can read and anything but a regular file.
func (f KeyFile) Load(id string) (*Key, error) {
	data, err := f.read()
	if err != nil || data == nil {
		return nil, err
	}
	for _, s := range data.Keys {
		if s.ID != id {
			continue
		}
		seed, err := decode(s.SigningSeed, 32)
		if err != nil {
			return nil, f.damaged()
		}
		dh, err := decode(s.DHPrivate, 32)
		if err != nil {
			return nil, f.damaged()
		}
		k, err := fromSeeds(seed, dh)
		if err != nil || k.ID != id {
			return nil, f.damaged()
		}
		return k, nil
	}
	return nil, nil
}

// Add saves k in front of the keys already there, keeping at most maxKeys.
// A damaged or unreadable file is replaced.
func (f KeyFile) Add(k *Key, now time.Time) error {
	data, err := f.read()
	if err != nil || data == nil {
		data = &keyFileData{}
	}
	keys := []savedKey{{
		ID:          k.ID,
		SigningSeed: encode(k.signing.Seed()),
		DHPrivate:   encode(k.dh.Bytes()),
		CreatedAt:   now.UTC(),
	}}
	for _, s := range data.Keys {
		if s.ID != k.ID && len(keys) < maxKeys {
			keys = append(keys, s)
		}
	}
	return f.write(&keyFileData{Keys: keys})
}

// Keep removes every key but id: run once a login's new sign-in is saved,
// so an interrupted login leaves the previous key in place.
func (f KeyFile) Keep(id string) error {
	data, err := f.read()
	if err != nil || data == nil {
		return err
	}
	var keys []savedKey
	for _, s := range data.Keys {
		if s.ID == id {
			keys = append(keys, s)
		}
	}
	if len(keys) == len(data.Keys) {
		return nil
	}
	if len(keys) == 0 {
		return f.Remove()
	}
	return f.write(&keyFileData{Keys: keys})
}

// Remove deletes the file (signing out). A missing file is not an error.
func (f KeyFile) Remove() error {
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", f.Path, err)
	}
	return nil
}

func (f KeyFile) damaged() error {
	return fmt.Errorf("%s is damaged — run `astrona login` to make a new device key", f.Path)
}

func (f KeyFile) read() (*keyFileData, error) {
	info, err := os.Lstat(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading your device key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file — remove it, then run `astrona login`", f.Path)
	}
	file, err := os.Open(f.Path)
	if err != nil {
		return nil, fmt.Errorf("reading your device key: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading your device key: %w", err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while it was being read — try again", f.Path)
	}
	if runtime.GOOS != "windows" && opened.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users on this machine (mode %04o) — it holds this computer's device key. "+
			"Fix it with `chmod 600 %s`", f.Path, opened.Mode().Perm(), f.Path)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading your device key: %w", err)
	}
	var data keyFileData
	if len(raw) > maxFileSize || json.Unmarshal(raw, &data) != nil {
		return nil, f.damaged()
	}
	return &data, nil
}

// write saves data atomically: a 0600 temp file in the same directory,
// synced and renamed over the old one.
func (f KeyFile) write(data *keyFileData) (err error) {
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("saving your device key: %w", err)
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("saving your device key: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".device-key-*.tmp") // created 0600
	if err != nil {
		return fmt.Errorf("saving your device key: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return fmt.Errorf("saving your device key: %w", err)
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("saving your device key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("saving your device key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("saving your device key: %w", err)
	}
	if err := os.Rename(tmp.Name(), f.Path); err != nil {
		return fmt.Errorf("saving your device key: %w", err)
	}
	return nil
}
