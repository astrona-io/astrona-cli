package catalog

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const ats014 = `training:
  id: ATS014
  title: "ICA: Traffic Management"
modules:
  - id: module-010
    content:
      - type: reading
        title: Intro
        path: sections/section-010/README.md
      - type: lab
        title: "Route Requests By Header Lab"
        path: sections/section-010/module-01/labs/lab-01
      - type: lab
        title: "Reshape A Request Lab"
        path: sections/section-010/module-01/labs/lab-02/
`

func TestParseManifest(t *testing.T) {
	tr, err := ParseManifest([]byte(ats014), "https://github.com/astrona-io/ATS014.git", "ATS014")
	if err != nil {
		t.Fatal(err)
	}
	if tr.ID != "ATS014" || len(tr.Labs) != 2 {
		t.Fatalf("training = %+v", tr)
	}
	if l := tr.Labs[1]; l.ID != "ATS014/section-010/module-01/lab-02" || l.Path != "sections/section-010/module-01/labs/lab-02" {
		t.Errorf("lab = %+v", l)
	}
	for _, bad := range []string{"../../etc", "/etc/passwd", "a/../../b", `a\b`} {
		m := strings.Replace(ats014, "sections/section-010/module-01/labs/lab-01", bad, 1)
		if _, err := ParseManifest([]byte(m), "r", "ATS014"); err == nil {
			t.Errorf("lab path %q accepted", bad)
		}
	}
	if _, err := ParseManifest([]byte("training: {id: 'bad id!'}\n"), "r", "x"); err == nil {
		t.Error("bad training id accepted")
	}
	if tr, _ := ParseManifest([]byte("modules: []\n"), "r", "MyRepo"); tr.ID != "MyRepo" {
		t.Errorf("fallback id = %q", tr.ID)
	}
}

func TestFindSearchAndLabIDs(t *testing.T) {
	tr, _ := ParseManifest([]byte(ats014), "repo", "ATS014")
	c := Catalog{Trainings: []Training{tr}}
	if _, l, ok := c.Find("ats014/section-010/module-01/lab-01"); !ok || l.Title != "Route Requests By Header Lab" {
		t.Errorf("Find = %+v %v", l, ok)
	}
	if got := c.Search("reshape request"); len(got) != 1 || got[0].Lab.Path != "sections/section-010/module-01/labs/lab-02" {
		t.Errorf("Search = %+v", got)
	}
	for s, want := range map[string]bool{"ATS014/section-010/module-01/lab-02": true, "./labs/x": false, "labs": false, "https://x/y": false, "git@github.com:a/b": false, "/abs/path": false} {
		if LooksLikeLabID(s) != want {
			t.Errorf("LooksLikeLabID(%q) = %v", s, !want)
		}
	}
}

func TestFindWithOwnerAndByConvention(t *testing.T) {
	tr, _ := ParseManifest([]byte(ats014), "https://github.com/astrona-io/ATS014.git", "ATS014")
	c := Catalog{Trainings: []Training{tr}}
	// The owner may lead the name; it must be the repository's.
	if _, l, ok := c.Find("astrona-io/ATS014/section-010/module-01/lab-01"); !ok || l.ID != "ATS014/section-010/module-01/lab-01" {
		t.Errorf("Find with owner = %+v %v", l, ok)
	}
	if _, _, ok := c.Find("someone-else/ATS014/section-010/module-01/lab-01"); ok {
		t.Error("Find accepted another owner")
	}
	// astrona.io stands for the astrona-io GitHub owner, in any case.
	for _, id := range []string{"astrona.io/ATS014/section-010/module-01/lab-01", "Astrona.IO/ats014/section-010/module-01/lab-01"} {
		if _, l, ok := c.Find(id); !ok || l.ID != "ATS014/section-010/module-01/lab-01" {
			t.Errorf("Find(%q) = %+v %v", id, l, ok)
		}
	}
	other, _ := ParseManifest([]byte(ats014), "https://github.com/someone-else/ATS014.git", "ATS014")
	if _, _, ok := (Catalog{Trainings: []Training{other}}).Find("astrona.io/ATS014/section-010/module-01/lab-01"); ok {
		t.Error("astrona.io/ matched a training owned by someone else")
	}
	if !LooksLikeLabID("astrona.io/ATS014/section-010/module-01/lab-01") {
		t.Error("LooksLikeLabID rejects the astrona.io/ form")
	}
	// A folder the manifest does not list, by convention.
	for id, want := range map[string]string{
		"ATS014/section-010/module-01/playground":            "sections/section-010/module-01/playground",
		"astrona-io/ATS014/section-010/module-03/playground": "sections/section-010/module-03/playground",
		"astrona.io/ATS014/section-000/module-01/playground": "sections/section-000/module-01/playground",
	} {
		if _, l, ok := c.Find(id); !ok || l.Path != want {
			t.Errorf("Find(%q) = %+v %v, want path %s", id, l, ok, want)
		}
	}
	// Only a playground is found by convention: a mistyped lab is not.
	for _, id := range []string{"ATS999/section-010/module-01/playground", "ATS014/section-020/module-02/lab-03", "ATS014", "ATS014/../playground", "ATS014/a/../../playground"} {
		if _, l, ok := c.Find(id); ok {
			t.Errorf("Find(%q) = %+v, want not found", id, l)
		}
	}
}

func TestFetchFromGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/repositories"):
			if !strings.Contains(r.URL.RawQuery, "topic%3Aastrona-training") {
				t.Errorf("search query = %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"items":[{"clone_url":"https://github.com/astrona-io/ATS014.git"},{"clone_url":"https://github.com/astrona-io/ATS999.git"}]}`))
		case r.URL.Path == "/astrona-io/ATS014/HEAD/astrona.yaml":
			w.Write([]byte(ats014))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := &Fetcher{Client: srv.Client(), API: srv.URL, Raw: srv.URL}
	c := f.Fetch("astrona-io", nil)
	if len(c.Trainings) != 1 || c.Trainings[0].ID != "ATS014" {
		t.Fatalf("trainings = %+v", c.Trainings)
	}
	if len(c.Errors) != 1 || !strings.Contains(c.Errors[0], "ATS999") || !strings.Contains(c.Errors[0], "no astrona.yaml") {
		t.Errorf("errors = %v", c.Errors)
	}
}

func TestStoreCacheAndSources(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Now()
	if _, ok := s.Cached(now); ok {
		t.Fatal("empty store is cached")
	}
	s.Save(Catalog{FetchedAt: now, Trainings: []Training{{ID: "A"}}})
	if c, ok := s.Cached(now.Add(30 * time.Minute)); !ok || c.Trainings[0].ID != "A" {
		t.Errorf("fresh cache = %+v %v", c, ok)
	}
	if _, ok := s.Cached(now.Add(2 * time.Hour)); ok {
		t.Error("stale cache used")
	}
	s.SetSources([]string{"https://git.example.com/me/training.git"})
	if src, _ := s.Sources(); len(src) != 1 {
		t.Errorf("sources = %v", src)
	}
	if _, ok := s.Cached(now); ok {
		t.Error("changing sources must drop the cache")
	}
}

// The sections layout: modules inside sections, plus a capstone list.
func TestParseManifestSections(t *testing.T) {
	m := `training: {id: ATS015, title: Securing Workloads}
sections:
  - id: section-010
    modules:
      - content:
          - {type: reading, title: R, path: sections/section-010/module-01/course.md}
          - {type: playground, title: P, path: sections/section-010/module-01/playground}
          - {type: lab, title: "Module 1 lab", path: sections/section-010/module-01/labs/lab-01}
    capstone:
      - {type: lab, title: "Capstone", path: sections/section-010/capstone/labs/lab-01}
`
	tr, err := ParseManifest([]byte(m), "r", "ATS015")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Labs) != 2 || tr.Labs[0].ID != "ATS015/section-010/module-01/lab-01" || tr.Labs[1].ID != "ATS015/section-010/capstone/lab-01" {
		t.Errorf("labs = %+v", tr.Labs)
	}
}

// A third-party source can't pass its labs off as an owner's: a training id
// that is (or looks like) an owner name is refused, and a name led by a
// known owner is only ever matched against that owner's repositories.
func TestFindRefusesOwnerSpoofing(t *testing.T) {
	official, _ := ParseManifest([]byte(ats014), "https://github.com/astrona-io/ATS014.git", "ATS014")
	for _, id := range []string{"astrona.io", "Astrona-IO", "astrona-io", "evil.io"} {
		m := strings.Replace(ats014, "id: ATS014", "id: "+id, 1)
		if _, err := ParseManifest([]byte(m), "https://github.com/evil/x.git", "x"); err == nil {
			t.Errorf("training id %q accepted", id)
		}
	}
	// Even a catalog that holds one (an old cache) doesn't resolve it.
	for _, spoofID := range []string{"astrona.io", "astrona-io"} {
		spoof := Training{ID: spoofID, Repo: "https://github.com/evil/x.git", Labs: []Lab{{
			ID: spoofID + "/ATS014/section-010/module-01/lab-02", Path: "ATS014/section-010/module-01/lab-02",
		}}}
		for _, ts := range [][]Training{{spoof}, {spoof, official}} {
			c := Catalog{Trainings: ts}
			for _, id := range []string{spoof.Labs[0].ID, spoofID + "/ATS014/section-010/module-09/playground"} {
				if tr, _, ok := c.Find(id); ok && tr.Repo == spoof.Repo {
					t.Errorf("Find(%q) resolved to the spoofing repository", id)
				}
			}
		}
		// A cached catalog is cleaned the same way when it's read.
		s := Store{Dir: t.TempDir()}
		s.Save(Catalog{FetchedAt: time.Now(), Trainings: []Training{spoof, official}})
		if c, ok := s.Cached(time.Now()); !ok || len(c.Trainings) != 1 || c.Trainings[0].Repo != official.Repo {
			t.Errorf("cached catalog = %+v %v", c.Trainings, ok)
		}
	}
}

// Two sources with the same training id: the default org's wins, whatever
// order they were read in, and the other is reported.
func TestDuplicateTrainingIDsPreferDefaultOrg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/repositories"):
			w.Write([]byte(`{"items":[{"clone_url":"https://github.com/astrona-io/ATS014.git"}]}`))
		case strings.HasSuffix(r.URL.Path, "/HEAD/astrona.yaml"):
			w.Write([]byte(ats014))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := &Fetcher{Client: srv.Client(), API: srv.URL, Raw: srv.URL}
	for range 20 {
		c := f.Fetch("astrona-io", []string{"https://github.com/aaa/ATS014.git", "https://github.com/zzz/ats014.git"})
		if len(c.Trainings) != 1 || c.Trainings[0].Repo != "https://github.com/astrona-io/ATS014.git" {
			t.Fatalf("trainings = %+v", c.Trainings)
		}
		if len(c.Errors) != 2 || !strings.Contains(c.Errors[0], "https://github.com/astrona-io/ATS014.git") {
			t.Fatalf("errors = %v", c.Errors)
		}
		if tr, _, ok := c.Find("ATS014/section-010/module-01/lab-02"); !ok || !tr.Official() {
			t.Fatalf("Find = %+v %v", tr, ok)
		}
	}
	// Without the default org, the first repository by URL wins.
	c := Catalog{Trainings: []Training{{ID: "T1", Repo: "https://github.com/b/t1.git"}, {ID: "t1", Repo: "https://github.com/a/t1.git"}}}
	c.normalize()
	if len(c.Trainings) != 1 || c.Trainings[0].Repo != "https://github.com/a/t1.git" {
		t.Errorf("trainings = %+v", c.Trainings)
	}
}
