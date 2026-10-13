package labstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Submission marks a submitted run without removing its working environment.
type Submission struct {
	At        time.Time `json:"at"`
	Pass      bool      `json:"pass"`
	WallClock bool      `json:"wall_clock,omitempty"` // No UI activity existed at submission.
}

// ReadSubmission returns the run's completion marker, or nil before submission.
func ReadSubmission(lab, started string) (*Submission, error) {
	if lab == "" || started == "" {
		return nil, nil
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "submission", "result.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result Submission
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.At.IsZero() {
		return nil, fmt.Errorf("submission has no completion time")
	}
	return &result, nil
}

// SaveSubmission finishes a run once, preserving its first submitted result.
func SaveSubmission(lab, started string, pass bool, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("submission requires a completion time")
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return err
	}
	dir = filepath.Join(dir, "submission")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "result.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	previous, err := ReadSubmission(lab, started)
	if err != nil {
		return err
	}
	if previous != nil {
		return nil
	}
	_, tracked, err := ActiveTime(lab, started)
	if err != nil {
		return err
	}
	data, err := json.Marshal(Submission{At: at.UTC(), Pass: pass, WallClock: !tracked})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "result.json"), data)
}
