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
	// maxSiteCatalog bounds the site's catalog answer: every training at once.
	maxSiteCatalog = 16 << 20
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
	// Site is the Astrona site whose catalog (GET {Site}/api/labs) lists
	// DefaultOrg's trainings. Empty searches GitHub for them instead.
	Site string
}

// NewFetcher talks to github.com.
func NewFetcher(clone func(string) ([]byte, error)) *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 15 * time.Second}, API: "https://api.github.com", Raw: "https://raw.githubusercontent.com", Clone: clone}
}

// Fetch builds the catalog: org's repos with the training topic, plus
// extra sources (git URLs). DefaultOrg's trainings come from the Site's
// catalog when one is set, so a student never hits GitHub's search limits.
// A source that fails is listed in Errors.
func (f *Fetcher) Fetch(org string, extra []string) Catalog {
	cat := Catalog{FetchedAt: time.Now(), Trainings: []Training{}}
	var repos []string
	if org == DefaultOrg && f.Site != "" {
		found, err := f.siteTrainings()
		if err != nil {
			cat.Errors = append(cat.Errors, fmt.Sprintf("lab catalog of %s: %s", f.Site, err))
		}
		cat.Trainings = append(cat.Trainings, found...)
	} else if org != "" {
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
	for _, t := range cat.Trainings {
		seen[t.Repo] = true // an extra source naming a site training isn't read again
	}
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
	cat.normalize()
	return cat
}

// siteTrainings reads DefaultOrg's trainings from the site's catalog, which
// Astrona syncs from the same repositories. The site names each repository,
// never its URL: every training is a github.com/DefaultOrg repository, and
// lab paths and ids are checked like a manifest's.
func (f *Fetcher) siteTrainings() ([]Training, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(f.Site, "/")+"/api/labs", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "astrona-cli")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	var out struct {
		Trainings []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Repo        string `json:"repo"`
			Labs        []struct {
				Title string `json:"title"`
				Path  string `json:"path"`
			} `json:"labs"`
		} `json:"trainings"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSiteCatalog+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSiteCatalog {
		return nil, fmt.Errorf("the catalog is larger than %d MB", maxSiteCatalog>>20)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("the catalog isn't valid JSON: %w", err)
	}
	if out.Trainings == nil {
		return nil, errors.New("the answer has no catalog")
	}
	trainings := make([]Training, 0, len(out.Trainings))
	for _, st := range out.Trainings {
		name := strings.TrimSpace(st.Repo)
		if !idPattern.MatchString(name) {
			continue
		}
		t := Training{
			ID:          strings.TrimSpace(st.ID),
			Title:       strings.TrimSpace(st.Title),
			Description: strings.TrimSpace(st.Description),
			Repo:        "https://github.com/" + DefaultOrg + "/" + name + ".git",
			Labs:        []Lab{},
		}
		if t.ID == "" {
			t.ID = name
		}
		for _, l := range st.Labs {
			p, ok := cleanLabPath(l.Path)
			if !ok {
				continue
			}
			t.Labs = append(t.Labs, Lab{ID: LabID(t.ID, p), Title: strings.TrimSpace(l.Title), Path: p})
		}
		trainings = append(trainings, t)
	}
	return trainings, nil
}

// The search API returns at most 100 results a page and 1,000 in all.
const (
	searchPerPage  = 100
	searchMaxPages = 10
)

// topicRepos lists org's public repos with the training topic, as clone URLs.
// It reads every page; a page that fails fails the whole search, so the
// catalog is never built from part of the list.
func (f *Fetcher) topicRepos(org string) ([]string, error) {
	q := url.QueryEscape("org:" + org + " topic:" + Topic)
	var repos []string
	seen := map[string]bool{}
	for page := 1; page <= searchMaxPages; page++ {
		items, total, err := f.searchPage(q, page)
		if err != nil {
			if page > 1 {
				return nil, fmt.Errorf("page %d: %w", page, err)
			}
			return nil, err
		}
		for _, u := range items {
			if !seen[u] {
				seen[u] = true
				repos = append(repos, u)
			}
		}
		if len(items) < searchPerPage || (total > 0 && len(repos) >= total) {
			break
		}
	}
	return repos, nil
}

// searchPage reads one page of a repository search: its clone URLs and the
// search's total_count.
func (f *Fetcher) searchPage(q string, page int) ([]string, int, error) {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/search/repositories?per_page=%d&page=%d&q=%s", f.API, searchPerPage, page, q), nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("%s", resp.Status)
	}
	var out struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			CloneURL string `json:"clone_url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, 0, err
	}
	urls := make([]string, 0, len(out.Items))
	for _, it := range out.Items {
		urls = append(urls, it.CloneURL)
	}
	return urls, out.TotalCount, nil
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
	c.normalize() // a cache written by an older astrona may hold what Fetch now drops
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
	if json.Unmarshal(data, &c) != nil {
		return Catalog{}, false
	}
	c.normalize()
	return c, true
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
