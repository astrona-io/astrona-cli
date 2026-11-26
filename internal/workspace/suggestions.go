package workspace

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"astrona/internal/catalog"
	"astrona/internal/labops"
)

// suggestionsCacheFor is how long the server reuses a catalog it read.
const suggestionsCacheFor = 10 * time.Minute

// maxSuggestions bounds GET /catalog/suggestions.
const maxSuggestions = 5

// catalogCache keeps the last catalog Config.Catalog returned, so the page
// asking again does not read it again. Failures are not kept.
type catalogCache struct {
	mu  sync.Mutex
	at  time.Time
	cat catalog.Catalog
}

func (c *catalogCache) get(load func() (catalog.Catalog, error), now time.Time) (catalog.Catalog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && now.Sub(c.at) < suggestionsCacheFor {
		return c.cat, nil
	}
	cat, err := load()
	if err != nil {
		return cat, err
	}
	c.cat, c.at = cat, now
	return cat, nil
}

// catalogSuggestion is one lab GET /catalog/suggestions suggests.
type catalogSuggestion struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Kind       string `json:"kind"`
	URL        string `json:"url"`
	RunCommand string `json:"runCommand"`
}

// handleCatalogSuggestions is GET /catalog/suggestions?lab=<lab>: up to
// maxSuggestions other labs of the run's training section, from the
// catalog the CLI reads (`astrona labs`). A lab from the student's own
// files has none. Links go to the CLI's own site, never one from lab
// content. A catalog that cannot be read is 502 catalog_unavailable, never
// an empty list.
func (s *Server) handleCatalogSuggestions(w http.ResponseWriter, r *http.Request) {
	rec, ok := loadRun(w, r.URL.Query().Get("lab"))
	if !ok {
		return
	}
	site := strings.TrimRight(s.cfg.CatalogSite, "/")
	out := struct {
		Labs       []catalogSuggestion `json:"labs"`
		CatalogURL string              `json:"catalogUrl"`
	}{Labs: []catalogSuggestion{}}
	if site != "" {
		out.CatalogURL = site + "/labs"
	}
	if rec.CatalogLab == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if s.cfg.Catalog == nil {
		writeErrorCode(w, http.StatusBadGateway, "catalog_unavailable", "the lab catalog is not available")
		return
	}
	cat, err := s.catalog.get(s.cfg.Catalog, time.Now())
	if err != nil {
		writeErrorCode(w, http.StatusBadGateway, "catalog_unavailable", "the lab catalog could not be read: "+err.Error())
		return
	}
	t, l, found := cat.Find(rec.CatalogLab)
	if !found {
		if len(cat.Errors) > 0 {
			writeErrorCode(w, http.StatusBadGateway, "catalog_unavailable", "the lab catalog could not be read: "+cat.Errors[0])
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	for _, c := range suggestLabs(t, l) {
		sg := catalogSuggestion{
			ID:         c.ID,
			Title:      labops.SanitizeLine(c.Title),
			Kind:       "lab",
			RunCommand: "astrona run " + c.ID,
		}
		if sg.Title == "" {
			sg.Title = c.ID
		}
		if labops.IsPlaygroundName(c.ID) {
			sg.Kind = "playground"
		}
		if site != "" && t.Official() {
			sg.URL = site + "/labs/" + c.ID
		}
		out.Labs = append(out.Labs, sg)
	}
	writeJSON(w, http.StatusOK, out)
}

// suggestLabs are up to maxSuggestions labs of t in current's section (the
// first part of its catalog name after the training): the labs after it
// in manifest order first, then the ones before, and its own module's
// before the rest of the section.
func suggestLabs(t catalog.Training, current catalog.Lab) []catalog.Lab {
	section, module := labGroup(t.ID, current.ID)
	at := -1
	for i, l := range t.Labs {
		if strings.EqualFold(l.ID, current.ID) {
			at = i
			break
		}
	}
	ordered := append(append([]catalog.Lab{}, t.Labs[at+1:]...), t.Labs[:max(at, 0)]...)
	var same, rest []catalog.Lab
	for _, l := range ordered {
		s, m := labGroup(t.ID, l.ID)
		switch {
		case strings.EqualFold(l.ID, current.ID) || !strings.EqualFold(s, section):
		case module != "" && strings.EqualFold(m, module):
			same = append(same, l)
		default:
			rest = append(rest, l)
		}
	}
	out := append(same, rest...)
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// labGroup is the section and module of a catalog name
// (ATS014/section-010/module-01/lab-02 → section-010, module-01); module is
// "" for a lab directly under its section.
func labGroup(trainingID, id string) (section, module string) {
	rest := id
	if len(id) > len(trainingID) && strings.EqualFold(id[:len(trainingID)+1], trainingID+"/") {
		rest = id[len(trainingID)+1:]
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 0 {
		section = parts[0]
	}
	if len(parts) > 2 {
		module = parts[1]
	}
	return section, module
}
