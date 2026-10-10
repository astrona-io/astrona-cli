package labstate

import "fmt"

// Contexts is the user's own kubeconfig as kubectl sees it (respecting
// $KUBECONFIG). cluster.KubectlContexts is the real one; tests use a fake.
type Contexts interface {
	// Current returns the current-context; set is false when there is none.
	Current() (name string, set bool, err error)
	Use(name string) error
	Unset() error
	Exists(name string) (bool, error)
}

// Switched is what SwitchContext did.
type Switched struct {
	Previous      string // the context restored by `astrona destroy`
	PreviousUnset bool   // there was no current-context before
	// AlreadyCurrent: the lab's context was already current and there was
	// nothing on record to go back to — nothing was changed or recorded.
	AlreadyCurrent bool
}

// SwitchContext makes labContext the current-context of the user's
// kubeconfig and records, in lab's state, the context to go back to. The
// record is saved before the switch, so it is never lost to a failure in
// between.
//
// A lab that is started over keeps its first record: by then the current
// context is the lab's own (or unset — deleting a kind cluster unsets its
// context), which isn't what the user had before the lab.
func SwitchContext(kc Contexts, lab, labContext string) (Switched, error) {
	cur, set, err := kc.Current()
	if err != nil {
		return Switched{}, fmt.Errorf("could not read your kubectl current-context: %w", err)
	}
	st, err := Load(lab)
	if err != nil {
		return Switched{}, err
	}

	var rec *KubeContext
	switch {
	case st != nil && st.KubeContext != nil && st.KubeContext.Lab == labContext && (!set || cur == labContext):
		rec = st.KubeContext
	case set && cur == labContext:
		return Switched{AlreadyCurrent: true}, nil
	default:
		rec = &KubeContext{Lab: labContext, Previous: cur, PreviousUnset: !set}
	}
	if err := Update(lab, func(s *State) { s.KubeContext = rec }); err != nil {
		return Switched{}, fmt.Errorf("could not remember your kubectl context: %w", err)
	}
	if !set || cur != labContext {
		if err := kc.Use(labContext); err != nil {
			return Switched{}, fmt.Errorf("could not switch kubectl to %s: %w", labContext, err)
		}
	}
	return Switched{Previous: rec.Previous, PreviousUnset: rec.PreviousUnset}, nil
}

// RestoreOutcome is what RestoreContext found and did.
type RestoreOutcome int

const (
	// NothingRecorded: run didn't switch the context for this lab.
	NothingRecorded RestoreOutcome = iota
	// Restored: the previous context is current again.
	Restored
	// RestoredUnset: there was none before, so current-context is unset again.
	RestoredUnset
	// MovedOn: the user switched away from the lab meanwhile — left alone.
	MovedOn
	// PreviousGone: the previous context no longer exists — left alone.
	PreviousGone
)

// RestoreResult is RestoreContext's result: what happened, the previous
// context, and (MovedOn) the context the user is on now.
type RestoreResult struct {
	Outcome  RestoreOutcome
	Previous string
	Current  string
}

// RestoreContext puts back the context recorded by SwitchContext — only
// when the lab's context is still current, since a user who switched
// elsewhere meanwhile chose that. The record is dropped either way. Call
// it before the cluster is deleted: deleting a kind cluster unsets its
// context, after which "is it still current?" can't be answered.
//
// Labs started on top of each other chain: B, started while A's context
// was current, remembers A's context. When A goes first, B is handed A's
// own previous context, so destroying B still gets the user back to where
// they were before either lab.
func RestoreContext(kc Contexts, lab string) (RestoreResult, error) {
	st, err := Load(lab)
	if err != nil {
		return RestoreResult{}, err
	}
	if st == nil || st.KubeContext == nil {
		return RestoreResult{Outcome: NothingRecorded}, nil
	}
	rec := *st.KubeContext
	res, err := restore(kc, rec)
	if err != nil {
		return res, err
	}
	if err := Update(lab, func(s *State) { s.KubeContext = nil }); err != nil {
		return res, err
	}
	if err := handOver(lab, rec); err != nil {
		return res, err
	}
	return res, nil
}

// handOver gives every other lab that remembers gone's context as the one
// to go back to gone's own previous context instead.
func handOver(gone string, rec KubeContext) error {
	names, err := Names()
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == gone {
			continue
		}
		st, err := Load(name)
		if err != nil || st == nil || st.KubeContext == nil || st.KubeContext.PreviousUnset ||
			st.KubeContext.Previous != rec.Lab || st.KubeContext.Lab == rec.Previous {
			continue
		}
		if err := Update(name, func(s *State) {
			if s.KubeContext != nil {
				s.KubeContext.Previous, s.KubeContext.PreviousUnset = rec.Previous, rec.PreviousUnset
			}
		}); err != nil {
			return fmt.Errorf("could not hand your previous kubectl context on to %s: %w", name, err)
		}
	}
	return nil
}

func restore(kc Contexts, rec KubeContext) (RestoreResult, error) {
	res := RestoreResult{Previous: rec.Previous}
	cur, set, err := kc.Current()
	if err != nil {
		return res, fmt.Errorf("could not read your kubectl current-context: %w", err)
	}
	if !set || cur != rec.Lab {
		res.Outcome, res.Current = MovedOn, cur
		return res, nil
	}
	if rec.PreviousUnset {
		if err := kc.Unset(); err != nil {
			return res, fmt.Errorf("could not unset your kubectl current-context: %w", err)
		}
		res.Outcome = RestoredUnset
		return res, nil
	}
	ok, err := kc.Exists(rec.Previous)
	if err != nil {
		return res, fmt.Errorf("could not list your kubectl contexts: %w", err)
	}
	if !ok {
		res.Outcome = PreviousGone
		return res, nil
	}
	if err := kc.Use(rec.Previous); err != nil {
		return res, fmt.Errorf("could not switch kubectl back to %q: %w", rec.Previous, err)
	}
	res.Outcome = Restored
	return res, nil
}
