package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultOrg and Topic: the trainings astrona finds by default.
	DefaultOrg = "astrona-io"
	Topic      = "astrona-training"
	// cacheFor is how long a fetched catalog is reused.
	cacheFor = time.Hour
	// maxManifest bounds an astrona.yaml download.
	maxManifest = 4 << 20
)

// Fetcher reads trainings from the network; its fields are swappable for
// tests.
type Fetcher struct {
	Client *http.Client
	// API and Raw are GitHub's API and raw-content base URLs.
	API, Raw string
	// Clone reads astrona.yaml from a non-GitHub git source (cloned into
	// astrona's git cache); nil disables such sources.
	Clone func(gitURL string) ([]byte, error)
}

// NewFetcher talks to github.com.
func NewFetcher(clone func(string) ([]byte, error)) *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 15 * time.Second}, API: "https://api.github.com", Raw: "https://raw.githubusercontent.com", Clone: clone}
}

// Fetch builds the catalog: org's repos with the training topic, plus
// extra sources (git URLs). A source that fails is listed in Errors.
func (f *Fetcher) Fetch(org string, extra []string) Catalog {
	cat := Catalog{FetchedAt: time.Now(), Trainings: []Training{}}
	var repos []string
	if org != "" {
		found, err := f.topicRepos(org)
		if err != nil {
			cat.Errors = append(cat.Errors, fmt.Sprintf("GitHub search for %s trainings: %s", org, err))
		}
		repos = append(repos, found...)
	}
	repos = append(repos, extra...)

	var mu sync.Mutex
	var wg sync.WaitGroup
	seen := map[string]bool{}
	for _, r := range repos {
		if seen[r] {
			continue
		}
		seen[r] = true
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			t, err := f.training(repo)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				cat.Errors = append(cat.Errors, err.Error())
				return
			}
			cat.Trainings = append(cat.Trainings, t)
		}(r)
	}
	wg.Wait()
	sort.Slice(cat.Trainings, func(i, j int) bool { return cat.Trainings[i].ID < cat.Trainings[j].ID })
	sort.Strings(cat.Errors)
	return cat
}

// topicRepos lists org's public repos with the training topic, as clone URLs.
func (f *Fetcher) topicRepos(org string) ([]string, error) {
	q := url.QueryEscape("org:" + org + " topic:" + Topic)
	req, _ := http.NewRequest(http.MethodGet, f.API+"/search/repositories?per_page=100&q="+q, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	var out struct {
		Items []struct {
			CloneURL string `json:"clone_url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	var repos []string
	for _, it := range out.Items {
		repos = append(repos, it.CloneURL)
	}
	return repos, nil
}

// training reads one repo's astrona.yaml: straight from GitHub for a
// github.com repo, else through a clone.
func (f *Fetcher) training(repo string) (Training, error) {
	name := strings.TrimSuffix(filepath.Base(strings.TrimRight(repo, "/")), ".git")
	if owner, r, ok := githubRepo(repo); ok {
		req, _ := http.NewRequest(http.MethodGet, f.Raw+"/"+owner+"/"+r+"/HEAD/astrona.yaml", nil)
		resp, err := f.Client.Do(req)
		if err != nil {
			return Training{}, fmt.Errorf("%s: %w", repo, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return Training{}, fmt.Errorf("%s has no astrona.yaml", repo)
		}
		if resp.StatusCode != http.StatusOK {
			return Training{}, fmt.Errorf("%s: astrona.yaml: %s", repo, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifest+1))
		if err != nil {
			return Training{}, fmt.Errorf("%s: %w", repo, err)
		}
		if len(data) > maxManifest {
			return Training{}, fmt.Errorf("%s: astrona.yaml is larger than %d MB", repo, maxManifest>>20)
		}
		return ParseManifest(data, repo, name)
	}
	if f.Clone == nil {
		return Training{}, fmt.Errorf("%s: only GitHub sources are supported here", repo)
	}
	data, err := f.Clone(repo)
	if err != nil {
		return Training{}, fmt.Errorf("%s: %w", repo, err)
	}
	return ParseManifest(data, repo, name)
}

// githubRepo splits https://github.com/owner/repo(.git) or
// git@github.com:owner/repo(.git).
func githubRepo(repo string) (owner, name string, ok bool) {
	rest, found := strings.CutPrefix(repo, "https://github.com/")
	if !found {
		if rest, found = strings.CutPrefix(repo, "git@github.com:"); !found {
			return "", "", false
		}
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimRight(rest, "/"), ".git"), "/")
	if len(parts) != 2 || !idPattern.MatchString(parts[0]) || !idPattern.MatchString(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// Store is where the catalog cache and the user's sources live.
type Store struct{ Dir string }

func (s Store) cachePath() string   { return filepath.Join(s.Dir, "catalog.json") }
func (s Store) sourcesPath() string { return filepath.Join(s.Dir, "catalog-sources.json") }

// Cached returns the cached catalog if it's fresh enough.
func (s Store) Cached(now time.Time) (Catalog, bool) {
	data, err := os.ReadFile(s.cachePath())
	if err != nil {
		return Catalog{}, false
	}
	var c Catalog
	if json.Unmarshal(data, &c) != nil || now.Sub(c.FetchedAt) > cacheFor {
		return Catalog{}, false
	}
	return c, true
}

// Any returns the cached catalog however old — the fallback when
// fetching fails (offline).
func (s Store) Any() (Catalog, bool) {
	data, err := os.ReadFile(s.cachePath())
	if err != nil {
		return Catalog{}, false
	}
	var c Catalog
	return c, json.Unmarshal(data, &c) == nil
}

// Save caches c.
func (s Store) Save(c Catalog) error {
	return s.write(s.cachePath(), c)
}

// Sources are the git URLs the user added.
func (s Store) Sources() ([]string, error) {
	data, err := os.ReadFile(s.sourcesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	return out, json.Unmarshal(data, &out)
}

// SetSources replaces the user's sources (and drops the cache, so they
// show up right away).
func (s Store) SetSources(src []string) error {
	_ = os.Remove(s.cachePath())
	if src == nil {
		src = []string{}
	}
	return s.write(s.sourcesPath(), src)
}

func (s Store) write(p string, v any) error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
