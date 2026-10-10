package labstate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestSaveLoadPermissionsAndRemove(t *testing.T) {
	home := withHome(t)
	want := &State{
		Source:  &Source{Config: "/labs/x"},
		Session: &Session{Site: "https://astrona.io", ID: "s1", Lab: "ATS/x", URL: "https://astrona.io/labs/x", ExpiresAt: "2026-10-07T20:00:00Z"},
	}
	if err := Save("astro-x", want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".astrona", "labs", "astro-x.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", di.Mode().Perm())
	}
	got, err := Load("astro-x")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v, %v", got, err)
	}

	// Update merges into what is there.
	if err := Update("astro-x", func(s *State) { s.KubeContext = &KubeContext{Lab: "kind-astro-x", Previous: "prod"} }); err != nil {
		t.Fatal(err)
	}
	got, _ = Load("astro-x")
	if got.Source == nil || got.Session == nil || got.KubeContext.Previous != "prod" {
		t.Errorf("after Update: %+v", got)
	}
	if names, _ := Names(); !reflect.DeepEqual(names, []string{"astro-x"}) {
		t.Errorf("Names = %v", names)
	}

	if err := Remove("astro-x"); err != nil {
		t.Fatal(err)
	}
	if got, err := Load("astro-x"); got != nil || err != nil {
		t.Errorf("after Remove: %+v, %v", got, err)
	}
	if err := Remove("astro-x"); err != nil {
		t.Errorf("second Remove: %v", err)
	}
}

func TestSessionTokenIsNeverStored(t *testing.T) {
	home := withHome(t)
	if err := Save("astro-x", &State{Session: &Session{Site: "https://astrona.io", ID: "s1"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".astrona", "labs", "astro-x.json"))
	// 9: kind, deadlineAt and watchdogPid (a playground's timer) are not secrets.
	if reflect.TypeOf(Session{}).NumField() != 9 {
		t.Fatal("Session gained a field — make sure it isn't a secret, then update this test")
	}
	if s := strings.ToLower(string(data)); strings.Contains(s, "token") {
		t.Errorf("state file mentions a token: %s", s)
	}
}

func TestEmptyStateRemovesFile(t *testing.T) {
	withHome(t)
	if err := Save("astro-x", &State{Source: &Source{Config: "."}}); err != nil {
		t.Fatal(err)
	}
	if err := Update("astro-x", func(s *State) { s.Source = nil }); err != nil {
		t.Fatal(err)
	}
	if names, _ := Names(); len(names) != 0 {
		t.Errorf("Names = %v, want none", names)
	}
}

func TestPathRejectsEscapingNames(t *testing.T) {
	withHome(t)
	for _, bad := range []string{"../x", "a/b", "", ".."} {
		if _, err := Path(bad); err == nil {
			t.Errorf("Path(%q) accepted", bad)
		}
	}
}

// fakeContexts is an in-memory kubeconfig.
type fakeContexts struct {
	current  string // "" = unset
	contexts map[string]bool
	calls    []string
	fail     error
}

func newFake(current string, others ...string) *fakeContexts {
	f := &fakeContexts{current: current, contexts: map[string]bool{}}
	if current != "" {
		f.contexts[current] = true
	}
	for _, o := range others {
		f.contexts[o] = true
	}
	return f
}

func (f *fakeContexts) Current() (string, bool, error) {
	if f.fail != nil {
		return "", false, f.fail
	}
	return f.current, f.current != "", nil
}

func (f *fakeContexts) Use(name string) error {
	f.calls = append(f.calls, "use "+name)
	if !f.contexts[name] {
		return errors.New("no context " + name)
	}
	f.current = name
	return nil
}

func (f *fakeContexts) Unset() error {
	f.calls = append(f.calls, "unset")
	f.current = ""
	return nil
}

func (f *fakeContexts) Exists(name string) (bool, error) { return f.contexts[name], nil }

func TestSwitchThenRestore(t *testing.T) {
	withHome(t)
	kc := newFake("prod", "kind-astro-x")
	sw, err := SwitchContext(kc, "astro-x", "kind-astro-x")
	if err != nil {
		t.Fatal(err)
	}
	if sw.Previous != "prod" || sw.PreviousUnset || kc.current != "kind-astro-x" {
		t.Fatalf("switch: %+v, current %q", sw, kc.current)
	}
	st, _ := Load("astro-x")
	if st.KubeContext == nil || st.KubeContext.Previous != "prod" {
		t.Fatalf("not recorded: %+v", st)
	}

	res, err := RestoreContext(kc, "astro-x")
	if err != nil || res.Outcome != Restored || kc.current != "prod" {
		t.Fatalf("restore: %+v, %v, current %q", res, err, kc.current)
	}
	if st, _ := Load("astro-x"); st != nil && st.KubeContext != nil {
		t.Errorf("record kept after restore: %+v", st.KubeContext)
	}
	// Nothing left to restore.
	if res, _ := RestoreContext(kc, "astro-x"); res.Outcome != NothingRecorded {
		t.Errorf("second restore: %+v", res)
	}
}

func TestSwitchFromNoContextRestoresUnset(t *testing.T) {
	withHome(t)
	kc := newFake("", "kind-astro-x")
	sw, err := SwitchContext(kc, "astro-x", "kind-astro-x")
	if err != nil || !sw.PreviousUnset {
		t.Fatalf("switch: %+v, %v", sw, err)
	}
	res, err := RestoreContext(kc, "astro-x")
	if err != nil || res.Outcome != RestoredUnset || kc.current != "" {
		t.Fatalf("restore: %+v, %v, current %q", res, err, kc.current)
	}
}

func TestRestoreLeavesAContextTheUserPicked(t *testing.T) {
	withHome(t)
	kc := newFake("prod", "kind-astro-x", "staging")
	if _, err := SwitchContext(kc, "astro-x", "kind-astro-x"); err != nil {
		t.Fatal(err)
	}
	kc.current = "staging" // the user moved on
	kc.calls = nil
	res, err := RestoreContext(kc, "astro-x")
	if err != nil || res.Outcome != MovedOn || res.Current != "staging" {
		t.Fatalf("restore: %+v, %v", res, err)
	}
	if kc.current != "staging" || len(kc.calls) != 0 {
		t.Errorf("kubeconfig touched: current %q, calls %v", kc.current, kc.calls)
	}
}

func TestRestoreWhenPreviousIsGone(t *testing.T) {
	withHome(t)
	kc := newFake("old-cluster", "kind-astro-x")
	if _, err := SwitchContext(kc, "astro-x", "kind-astro-x"); err != nil {
		t.Fatal(err)
	}
	delete(kc.contexts, "old-cluster")
	kc.calls = nil
	res, err := RestoreContext(kc, "astro-x")
	if err != nil || res.Outcome != PreviousGone || res.Previous != "old-cluster" {
		t.Fatalf("restore: %+v, %v", res, err)
	}
	if kc.current != "kind-astro-x" || len(kc.calls) != 0 {
		t.Errorf("kubeconfig touched: current %q, calls %v", kc.current, kc.calls)
	}
}

// Two labs started on top of each other get the user back to their own
// context whichever is destroyed first.
func TestOverlappingLabsRestoreTheOriginalContext(t *testing.T) {
	for _, order := range [][]string{{"astro-a", "astro-b"}, {"astro-b", "astro-a"}} {
		t.Run(strings.Join(order, " then "), func(t *testing.T) {
			withHome(t)
			kc := newFake("prod", "kind-astro-a", "kind-astro-b")
			for _, lab := range []string{"astro-a", "astro-b"} {
				if _, err := SwitchContext(kc, lab, "kind-"+lab); err != nil {
					t.Fatal(err)
				}
			}
			for _, lab := range order {
				if _, err := RestoreContext(kc, lab); err != nil {
					t.Fatal(err)
				}
				// destroy: kind deletes the context (unsetting it when current), then the state goes.
				delete(kc.contexts, "kind-"+lab)
				if kc.current == "kind-"+lab {
					kc.current = ""
				}
				if err := Remove(lab); err != nil {
					t.Fatal(err)
				}
			}
			if kc.current != "prod" {
				t.Errorf("current = %q, want prod", kc.current)
			}
		})
	}
}

func TestStartOverKeepsTheFirstRecord(t *testing.T) {
	withHome(t)
	kc := newFake("prod", "kind-astro-x")
	if _, err := SwitchContext(kc, "astro-x", "kind-astro-x"); err != nil {
		t.Fatal(err)
	}
	// Starting over deletes the cluster (kind unsets its context), then
	// creates it again.
	kc.current = ""
	sw, err := SwitchContext(kc, "astro-x", "kind-astro-x")
	if err != nil || sw.Previous != "prod" || sw.PreviousUnset {
		t.Fatalf("second switch: %+v, %v", sw, err)
	}
	// Still on the lab: also kept.
	sw, err = SwitchContext(kc, "astro-x", "kind-astro-x")
	if err != nil || sw.Previous != "prod" {
		t.Fatalf("third switch: %+v, %v", sw, err)
	}
	// A stale record (the user deleted the lab by hand and moved on) is
	// replaced by what's current now.
	kc.contexts["staging"] = true
	kc.current = "staging"
	sw, err = SwitchContext(kc, "astro-x", "kind-astro-x")
	if err != nil || sw.Previous != "staging" {
		t.Fatalf("after moving on: %+v, %v", sw, err)
	}
}

func TestSwitchWhenAlreadyOnTheLab(t *testing.T) {
	withHome(t)
	kc := newFake("kind-astro-x")
	sw, err := SwitchContext(kc, "astro-x", "kind-astro-x")
	if err != nil || !sw.AlreadyCurrent {
		t.Fatalf("switch: %+v, %v", sw, err)
	}
	if st, _ := Load("astro-x"); st != nil {
		t.Errorf("recorded %+v — destroy would have nothing true to restore", st)
	}
}

func TestSwitchFailureLeavesNothingBroken(t *testing.T) {
	withHome(t)
	kc := newFake("prod")
	kc.fail = errors.New("kubeconfig unreadable")
	if _, err := SwitchContext(kc, "astro-x", "kind-astro-x"); err == nil {
		t.Fatal("expected an error")
	}
	if kc.current != "prod" {
		t.Errorf("current changed to %q", kc.current)
	}
}
