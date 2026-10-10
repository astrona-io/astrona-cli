// Package catalog finds astrona trainings and their labs: git repositories
// with an astrona.yaml manifest (training id/title, modules, and content
// entries of type "lab" with a path to the lab's directory).
//
// Trainings come from a GitHub search (repos in an organization with the
// "astrona-training" topic) and from sources the user adds. Everything a
// manifest says is untrusted data: lab paths are checked to stay inside
// the repository, and running a lab still goes through astrona's trust
// prompt like any remote lab.
package catalog

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Lab is one lab of a training.
type Lab struct {
	ID    string `json:"id"`    // e.g. ATS014/section-010/module-01/lab-02
	Title string `json:"title"` // from the manifest
	Path  string `json:"path"`  // directory in the repository
}

// Training is one training repository.
type Training struct {
	ID          string `json:"id"` // e.g. ATS014
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Repo        string `json:"repo"` // git URL to clone
	Labs        []Lab  `json:"labs"`
}

// Catalog is every training found, with when it was fetched.
type Catalog struct {
	FetchedAt time.Time  `json:"fetchedAt"`
	Trainings []Training `json:"trainings"`
	// Errors are sources that couldn't be read (shown, not fatal).
	Errors []string `json:"errors,omitempty"`
}

// manifest is the part of astrona.yaml the catalog reads. Two layouts
// exist: top-level modules, or sections holding modules plus a capstone.
type manifest struct {
	Training struct {
		ID          string `yaml:"id"`
		Title       string `yaml:"title"`
		Description string `yaml:"description"`
	} `yaml:"training"`
	Modules  []manifestModule `yaml:"modules"`
	Sections []struct {
		Modules  []manifestModule  `yaml:"modules"`
		Capstone []manifestContent `yaml:"capstone"`
	} `yaml:"sections"`
}

type manifestModule struct {
	Content []manifestContent `yaml:"content"`
}

type manifestContent struct {
	Type  string `yaml:"type"`
	Title string `yaml:"title"`
	Path  string `yaml:"path"`
}

// content is every content entry, in manifest order, in either layout.
func (m manifest) content() []manifestContent {
	var out []manifestContent
	for _, mod := range m.Modules {
		out = append(out, mod.Content...)
	}
	for _, sec := range m.Sections {
		for _, mod := range sec.Modules {
			out = append(out, mod.Content...)
		}
		out = append(out, sec.Capstone...)
	}
	return out
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ParseManifest reads a training's astrona.yaml. fallbackID (the repo
// name) is used when the manifest has no training id. Lab paths that would
// leave the repository are rejected.
func ParseManifest(data []byte, repo, fallbackID string) (Training, error) {
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Training{}, fmt.Errorf("astrona.yaml of %s isn't valid YAML: %w", repo, err)
	}
	id := strings.TrimSpace(m.Training.ID)
	if id == "" {
		id = fallbackID
	}
	if err := checkTrainingID(id); err != nil {
		return Training{}, fmt.Errorf("astrona.yaml of %s: %w", repo, err)
	}
	t := Training{ID: id, Title: strings.TrimSpace(m.Training.Title), Description: strings.TrimSpace(m.Training.Description), Repo: repo, Labs: []Lab{}}
	for _, c := range m.content() {
		if c.Type != "lab" {
			continue
		}
		p, ok := cleanLabPath(c.Path)
		if !ok {
			return Training{}, fmt.Errorf("astrona.yaml of %s: lab path %q leaves the repository", repo, c.Path)
		}
		t.Labs = append(t.Labs, Lab{ID: LabID(id, p), Title: strings.TrimSpace(c.Title), Path: p})
	}
	return t, nil
}

// checkTrainingID accepts a plain name that can't be mistaken for the owner
// a lab's catalog name may start with: no dots (astrona.io), not an owner
// name or alias — else any source could name its labs astrona.io/ATS014/….
func checkTrainingID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("training id %q isn't a plain name", id)
	}
	if strings.Contains(id, ".") || isOwnerName(id) {
		return fmt.Errorf("training id %q can't contain a dot or be an owner name like %s", id, DefaultOrg)
	}
	return nil
}

// isOwnerName reports whether s (any case) is DefaultOrg or an OwnerAliases
// name or owner.
func isOwnerName(s string) bool {
	if strings.EqualFold(s, DefaultOrg) {
		return true
	}
	for alias, owner := range OwnerAliases {
		if strings.EqualFold(s, alias) || strings.EqualFold(s, owner) {
			return true
		}
	}
	return false
}

// Official reports whether t's repository is on GitHub under DefaultOrg —
// the trainings whose labs start an Astrona lab session.
func (t Training) Official() bool { return sameTraining(t, DefaultOrg) }

// normalize drops trainings with an id checkTrainingID refuses (an older
// cache may hold them) and keeps one training per id (any case): the
// official one, else the first repository by URL. Every dropped training
// is recorded in Errors. Trainings end up sorted by id.
func (c *Catalog) normalize() {
	sort.SliceStable(c.Trainings, func(i, j int) bool {
		a, b := c.Trainings[i], c.Trainings[j]
		if x, y := strings.ToLower(a.ID), strings.ToLower(b.ID); x != y {
			return x < y
		}
		if a.Official() != b.Official() {
			return a.Official()
		}
		return a.Repo < b.Repo
	})
	kept := make([]Training, 0, len(c.Trainings))
	for _, t := range c.Trainings {
		if err := checkTrainingID(t.ID); err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("%s: %s — skipped", t.Repo, err))
			continue
		}
		if n := len(kept); n > 0 && strings.EqualFold(kept[n-1].ID, t.ID) {
			c.Errors = append(c.Errors, fmt.Sprintf("%s: training id %s is already used by %s — skipped", t.Repo, t.ID, kept[n-1].Repo))
			continue
		}
		kept = append(kept, t)
	}
	c.Trainings = kept
	sort.Strings(c.Errors)
}

// cleanLabPath accepts a relative path inside the repository.
func cleanLabPath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return "", false
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// LabID is a lab's catalog name: the training id and the lab's path
// without the "sections/" prefix and "labs/" segment —
// sections/section-010/module-01/labs/lab-02 → ATS014/section-010/module-01/lab-02.
func LabID(trainingID, labPath string) string {
	p := strings.TrimPrefix(labPath, "sections/")
	p = strings.ReplaceAll(p, "/labs/", "/")
	return trainingID + "/" + p
}

// OwnerAliases are names that stand for a GitHub owner at the start of a
// lab's catalog name: astrona.io/ATS014/… is astrona-io/ATS014/….
var OwnerAliases = map[string]string{"astrona.io": "astrona-io"}

// Find looks a lab up by its catalog name (training id case-insensitive).
// The name may start with the GitHub owner of the training's repository —
// astrona-io/ATS014/section-010/module-01/lab-02, or astrona.io/ATS014/…
// (OwnerAliases) — so it says where the code comes from; the owner must
// match. A name led by a known owner (DefaultOrg, OwnerAliases) is only
// ever looked up under that owner. A module's playground, which manifests do
// not list, is found by convention:
// ATS014/section-010/module-01/playground → sections/section-010/module-01/playground.
// Any other unlisted name is not found, so a typo still gets the catalog's hint.
// Whatever it resolves to is still fetched with git and trust-checked like
// any remote lab, and must hold a config.yaml to run.
func (c Catalog) Find(id string) (Training, Lab, bool) {
	owner, rest, ok := strings.Cut(id, "/")
	if !ok {
		return Training{}, Lab{}, false
	}
	if !isOwnerName(owner) {
		if t, l, ok := c.find(id); ok {
			return t, l, true
		}
	}
	if alias, ok := OwnerAliases[strings.ToLower(owner)]; ok {
		owner = alias
	}
	for _, t := range c.Trainings {
		if o, _, ok := githubRepo(t.Repo); ok && strings.EqualFold(o, owner) {
			if t, l, ok := c.find(rest); ok && sameTraining(t, o) {
				return t, l, true
			}
		}
	}
	return Training{}, Lab{}, false
}

func sameTraining(t Training, owner string) bool {
	o, _, ok := githubRepo(t.Repo)
	return ok && strings.EqualFold(o, owner)
}

// find is Find without the owner: a listed lab, else a playground by convention.
func (c Catalog) find(id string) (Training, Lab, bool) {
	for _, t := range c.Trainings {
		for _, l := range t.Labs {
			if strings.EqualFold(l.ID, id) {
				return t, l, true
			}
		}
	}
	training, rest, ok := strings.Cut(id, "/")
	if !ok || rest == "" || path.Base(rest) != "playground" {
		return Training{}, Lab{}, false
	}
	t, ok := c.Training(training)
	if !ok {
		return Training{}, Lab{}, false
	}
	p, ok := labPathFromID(rest)
	if !ok {
		return Training{}, Lab{}, false
	}
	return t, Lab{ID: LabID(t.ID, p), Title: rest, Path: p}, true
}

// labPathFromID is a playground's folder from the rest of its catalog name:
// section-010/module-01/playground → sections/section-010/module-01/playground.
func labPathFromID(rest string) (string, bool) {
	for _, part := range strings.Split(rest, "/") {
		if !idPattern.MatchString(part) {
			return "", false
		}
	}
	return cleanLabPath("sections/" + rest)
}

// Training looks a training up by id (case-insensitive).
func (c Catalog) Training(id string) (Training, bool) {
	for _, t := range c.Trainings {
		if strings.EqualFold(t.ID, id) {
			return t, true
		}
	}
	return Training{}, false
}

// Search finds labs whose title or id contains every word of q.
func (c Catalog) Search(q string) []struct {
	Training Training
	Lab      Lab
} {
	words := strings.Fields(strings.ToLower(q))
	var out []struct {
		Training Training
		Lab      Lab
	}
	for _, t := range c.Trainings {
		for _, l := range t.Labs {
			hay := strings.ToLower(l.Title + " " + l.ID + " " + t.Title)
			all := true
			for _, w := range words {
				all = all && strings.Contains(hay, w)
			}
			if all {
				out = append(out, struct {
					Training Training
					Lab      Lab
				}{t, l})
			}
		}
	}
	return out
}

// LooksLikeLabID reports whether s could be a catalog name (TRAINING/…)
// rather than a path or URL — callers check the disk first.
func LooksLikeLabID(s string) bool {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") {
		return false
	}
	first, rest, ok := strings.Cut(s, "/")
	return ok && rest != "" && idPattern.MatchString(first)
}
