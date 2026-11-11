package labops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"astrona/internal/account"
	"astrona/internal/filelock"
)

// Everything the CLI tells labs about a hands-on exam while it runs — a
// task's result, the task opened, a break started or ended, the student's
// activity — is an ExamSignal. One that can't be sent at once waits in
// ~/.astrona/exams/pending.json and goes out, in order, with the next
// exam command, the exam watchdog or the background sender every astrona
// command starts (`astrona outbox-flush`). The queue is apart from the
// attempt's local record, so starting a new attempt never loses what an
// ended one still has to send.
//
// A result is never dropped because it is late: labs takes a result
// graded before the end at any time. Only labs' definitive refusals (409
// after_end, 409 on_break, unknown task) or ExamSignalMaxAge drop it. A
// task open, break or activity signal labs refuses is dropped too: it
// can't become true later.

// Kinds of exam signal.
const (
	ExamSignalResult     = "result"
	ExamSignalOpen       = "open"
	ExamSignalBreakStart = "break_start"
	ExamSignalBreakStop  = "break_stop"
	ExamSignalActivity   = "activity"
)

// ExamSignalMaxAge is how long a signal waits before it is given up.
const ExamSignalMaxAge = 7 * 24 * time.Hour

// ExamSignal is one queued exam signal.
type ExamSignal struct {
	Attempt  string          `json:"attempt"`
	Site     string          `json:"site"`
	Kind     string          `json:"kind"`
	Position int             `json:"position,omitempty"`
	Key      string          `json:"key,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Source   string          `json:"source,omitempty"`
	At       time.Time       `json:"at"`
	Queued   time.Time       `json:"queued"`
}

// replaces reports whether s makes old pointless: a newer result of the
// same task (the latest wins on the server too), a newer task open, a
// newer activity.
func (s ExamSignal) replaces(old ExamSignal) bool {
	if s.Attempt != old.Attempt || s.Kind != old.Kind {
		return false
	}
	switch s.Kind {
	case ExamSignalResult:
		return s.Position == old.Position
	case ExamSignalOpen, ExamSignalActivity:
		return true
	}
	return false
}

func examQueuePath() (string, error) {
	path, err := ExamStatePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "pending.json"), nil
}

// updateExamQueue applies change to the queue under its lock.
func updateExamQueue(wait bool, change func([]ExamSignal) ([]ExamSignal, error)) (bool, error) {
	path, err := examQueuePath()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	release, ok, err := filelock.Lock(path+".lock", wait)
	if err != nil || !ok {
		return false, err
	}
	defer release()
	cur, err := readExamQueue(path)
	if err != nil {
		return true, err
	}
	next, err := change(cur)
	if err != nil {
		return true, err
	}
	if len(next) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, err
		}
		return true, nil
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return true, err
	}
	return true, writeExamFile(path, append(data, '\n'))
}

func readExamQueue(path string) ([]ExamSignal, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var q []ExamSignal
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return q, nil
}

// QueueExamSignal keeps sig until it is sent.
func QueueExamSignal(sig ExamSignal) error {
	if sig.Attempt == "" || sig.Site == "" {
		return errors.New("exam signal has no attempt or site")
	}
	if sig.Queued.IsZero() {
		sig.Queued = time.Now().UTC()
	}
	sig.At = sig.At.UTC()
	_, err := updateExamQueue(true, func(q []ExamSignal) ([]ExamSignal, error) {
		kept := q[:0]
		for _, old := range q {
			if !sig.replaces(old) {
				kept = append(kept, old)
			}
		}
		return append(kept, sig), nil
	})
	return err
}

// ExamQueueLen counts the signals waiting for attempt ("" for every
// attempt); results only when resultsOnly.
func ExamQueueLen(attempt string, resultsOnly bool) int {
	path, err := examQueuePath()
	if err != nil {
		return 0
	}
	q, _ := readExamQueue(path)
	n := 0
	for _, s := range q {
		if (attempt == "" || s.Attempt == attempt) && (!resultsOnly || s.Kind == ExamSignalResult) {
			n++
		}
	}
	return n
}

// migrateExamPending moves an older astrona's unsent results out of the
// local state into the queue, and removes a record kept only for them.
func migrateExamPending() {
	st, err := LoadExamState()
	if err != nil || st == nil || len(st.Pending) == 0 && !st.Ended {
		return
	}
	_ = UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s == nil {
			return nil, nil
		}
		for _, p := range s.Pending {
			sig := ExamSignal{Attempt: s.ID, Site: s.Site, Kind: ExamSignalResult, Position: p.Position, Key: p.Key, Result: p.Result, At: p.At}
			if err := QueueExamSignal(sig); err != nil {
				return s, err
			}
		}
		s.Pending = nil
		if s.Ended {
			return nil, nil // the clusters are gone: nothing else to keep
		}
		return s, nil
	})
}

// ExamFlush is what FlushExamQueue left.
type ExamFlush struct {
	Left      int  // signals still waiting
	SignedOut bool // some wait for a new sign-in (astrona login)
}

// FlushExamQueue sends the exam signals waiting, oldest first, each
// attempt's in order: one that can't be sent holds back that attempt's
// later ones. Refusals are said on warn (nil: not said).
func FlushExamQueue(ctx context.Context, warn io.Writer) ExamFlush {
	migrateExamPending()
	var res ExamFlush
	path, err := examQueuePath()
	if err != nil {
		return res
	}
	q, err := readExamQueue(path)
	if err != nil || len(q) == 0 {
		res.Left = len(q)
		return res
	}
	done := map[string]bool{} // by Queued+Kind+Key+Position
	id := func(s ExamSignal) string {
		return fmt.Sprintf("%s|%s|%s|%d|%s", s.Attempt, s.Kind, s.Key, s.Position, s.Queued.Format(time.RFC3339Nano))
	}
	held := map[string]bool{}
	apis := map[string]*ExamAPI{}
	for _, s := range q {
		if held[s.Attempt] {
			continue
		}
		if time.Since(s.Queued) > ExamSignalMaxAge {
			done[id(s)] = true
			if warn != nil && s.Kind == ExamSignalResult {
				fmt.Fprintf(warn, "Task %d's result from %s could not be sent within %d days; it was given up.\n", s.Position, s.At.Local().Format("Jan 2 15:04"), int(ExamSignalMaxAge.Hours()/24))
			}
			continue
		}
		if ctx.Err() != nil {
			break
		}
		api := apis[s.Site]
		if api == nil {
			if api, err = NewExamAPI(s.Site); err != nil {
				break
			}
			apis[s.Site] = api
		}
		sctx, cancel := context.WithTimeout(ctx, SignalTimeout)
		err := sendExamSignal(sctx, api, s)
		cancel()
		switch {
		case err == nil:
			done[id(s)] = true
		case s.Kind == ExamSignalResult && resultRefused(err):
			done[id(s)] = true
			if warn != nil {
				fmt.Fprintf(warn, "Task %d's result from %s was not accepted: %s\n", s.Position, s.At.Local().Format(time.Kitchen), err)
			}
		case s.Kind != ExamSignalResult && examAnswered(err):
			done[id(s)] = true // a refused open, break or activity can't become true later
		default:
			held[s.Attempt] = true
			if errors.Is(err, account.ErrSignedOut) {
				res.SignedOut = true
			}
		}
	}
	_, _ = updateExamQueue(true, func(cur []ExamSignal) ([]ExamSignal, error) {
		kept := cur[:0]
		for _, s := range cur {
			if !done[id(s)] {
				kept = append(kept, s)
			}
		}
		res.Left = len(kept)
		return kept, nil
	})
	return res
}

func sendExamSignal(ctx context.Context, api *ExamAPI, s ExamSignal) error {
	switch s.Kind {
	case ExamSignalResult:
		return api.PostResult(ctx, s.Attempt, s.Position, s.Result, s.Key, s.At, s.Source)
	case ExamSignalOpen:
		return api.OpenTask(ctx, s.Attempt, s.Position, s.At)
	case ExamSignalBreakStart:
		_, err := api.StartBreak(ctx, s.Attempt, s.At)
		return err
	case ExamSignalBreakStop:
		_, err := api.StopBreak(ctx, s.Attempt, s.At)
		return err
	case ExamSignalActivity:
		return api.Activity(ctx, s.Attempt, s.At, s.Key)
	}
	return fmt.Errorf("unknown exam signal %q (from a newer astrona?)", s.Kind) // kept until it is too old
}

// SignalExam queues sig and tries to send what waits at once. It fails
// only when sig could not be kept.
func SignalExam(ctx context.Context, sig ExamSignal, warn io.Writer) error {
	if err := QueueExamSignal(sig); err != nil {
		return err
	}
	FlushExamQueue(ctx, warn)
	return nil
}

// ExamActivityEvery is how often activity is reported at most.
const ExamActivityEvery = time.Minute

// NoteExamActivity reports that the student was working at at (an
// `astrona hands-on-exam` command, their shell in use), at most once a minute. It
// only queues: the caller's next flush sends it.
func NoteExamActivity(st *ExamState, at time.Time) error {
	if st == nil || st.Ended {
		return nil
	}
	due := false
	if err := UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s == nil || s.ID != st.ID || s.Ended {
			return s, nil
		}
		if !s.LastActivity.IsZero() && at.Sub(s.LastActivity) < ExamActivityEvery {
			return s, nil
		}
		s.LastActivity, due = at.UTC(), true
		return s, nil
	}); err != nil || !due {
		return err
	}
	return QueueExamSignal(ExamSignal{Attempt: st.ID, Site: st.Site, Kind: ExamSignalActivity, Key: newResultKey(), At: at})
}
