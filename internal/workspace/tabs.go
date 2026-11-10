package workspace

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"astrona/internal/config"
)

// Tab kinds, see docs/reference/workspace-api.md.
const (
	TabTerminal = config.TabTerminal
	TabBrowser  = config.TabBrowser
	TabDocs     = config.TabDocs
)

// Tab is one tab of the workspace's right panel. The lab decides the tabs;
// the page can switch between them, never add one. The first is the focus
// tab: the terminal on the lab's own cluster.
type Tab struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// Target is the target a terminal tab opens its shell on.
	Target string `json:"target,omitempty"`
	// URL is a browser tab's http(s)://127.0.0.1:<port>/… (a port forward of
	// the lab) or a docs tab's https URL.
	URL string `json:"url,omitempty"`
	// Doc is a docs tab's Markdown, shipped with the lab. It is served by
	// GET /runs/{lab}/tabs/{name}/doc, never in the run itself.
	Doc string `json:"doc,omitempty"`
}

// tabView is a tab as GET /runs/{lab} shows it.
type tabView struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Focus    bool   `json:"focus"`
	Target   string `json:"target,omitempty"`
	URL      string `json:"url,omitempty"`
	Document bool   `json:"document,omitempty"`
}

// FocusTab is the first tab: the terminal on the lab's own cluster.
func FocusTab(target string) Tab {
	return Tab{Name: config.FocusTabName, Title: "Terminal", Kind: TabTerminal, Target: target}
}

// DefaultTabs are the tabs of a lab that declares none: the focus tab, and
// for a multi-VM QEMU lab a terminal tab per further VM (vm-<name>, titled
// with the VM's name, the focus tab too). A linked Kind cluster gets a tab
// only when the lab lists it.
func DefaultTabs(targets []Target) []Tab {
	if len(targets) == 0 {
		return nil
	}
	tabs := []Tab{FocusTab(targets[0].Name)}
	if targets[0].Kind != TargetVM || len(targets) == 1 {
		return tabs
	}
	tabs[0].Title = targets[0].Label
	for _, t := range targets[1:] {
		if t.Kind == TargetVM {
			tabs = append(tabs, Tab{Name: VMTabName(t.Label), Title: t.Label, Kind: TabTerminal, Target: t.Name})
		}
	}
	return tabs
}

// VMTabName is the terminal tab of a multi-VM lab's VM named vm (its name
// in runtime.qemu); the first VM has the focus tab instead.
func VMTabName(vm string) string { return "vm-" + vm }

// tabs are the run's tabs; a record from before tabs gets DefaultTabs.
func (r RunRecord) tabs() []Tab {
	if len(r.Tabs) > 0 {
		return r.Tabs
	}
	return DefaultTabs(r.Targets)
}

func (r RunRecord) target(name string) (Target, bool) {
	for _, t := range r.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

func (r RunRecord) tab(name string) (Tab, bool) {
	for _, t := range r.tabs() {
		if t.Name == name {
			return t, true
		}
	}
	return Tab{}, false
}

func tabViews(tabs []Tab) []tabView {
	out := make([]tabView, 0, len(tabs))
	for i, t := range tabs {
		out = append(out, tabView{Name: t.Name, Title: t.Title, Kind: t.Kind, Focus: i == 0, Target: t.Target, URL: t.URL, Document: t.Kind == TabDocs && t.URL == ""})
	}
	return out
}

// validateTabs checks a record's tabs before it is written: the focus tab
// first, terminals only on the run's targets, browser tabs only on
// 127.0.0.1, docs only on https.
func validateTabs(rec RunRecord) error {
	if len(rec.Tabs) == 0 {
		return nil
	}
	if first := rec.Tabs[0]; first.Name != config.FocusTabName || first.Kind != TabTerminal || first.Target != rec.Targets[0].Name {
		return fmt.Errorf("the first tab must be the terminal on %s", rec.Targets[0].Name)
	}
	targets := map[string]bool{}
	for _, t := range rec.Targets {
		targets[t.Name] = true
	}
	seen := map[string]bool{}
	for _, t := range rec.Tabs {
		if err := config.ValidateName(t.Name); err != nil {
			return fmt.Errorf("tab name: %w", err)
		}
		if seen[t.Name] {
			return fmt.Errorf("tab %s is listed twice", t.Name)
		}
		seen[t.Name] = true
		switch t.Kind {
		case TabTerminal:
			if !targets[t.Target] {
				return fmt.Errorf("tab %s: %s is not a target of this run", t.Name, t.Target)
			}
		case TabBrowser:
			if _, err := browserOrigin(t.URL); err != nil {
				return fmt.Errorf("tab %s: %w", t.Name, err)
			}
		case TabDocs:
			if t.URL != "" {
				if _, err := config.TabDocsOrigin(t.URL); err != nil {
					return fmt.Errorf("tab %s: %w", t.Name, err)
				}
			}
		default:
			return fmt.Errorf("tab %s: unknown kind %q", t.Name, t.Kind)
		}
	}
	return nil
}

// browserOrigin is the origin of a browser tab's URL, which must be on
// 127.0.0.1: a port forward of the lab, never another host.
func browserOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() != loopbackAddr || u.User != nil {
		return "", fmt.Errorf("browser tab URL %q must be http(s)://127.0.0.1:<port>/", raw)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("browser tab URL %q needs a port", raw)
	}
	return u.Scheme + "://" + loopbackAddr + ":" + strconv.Itoa(port), nil
}

// BrowserTabURL is the URL a browser tab shows for a port forward.
func BrowserTabURL(scheme string, hostPort int, path string) string {
	return scheme + "://" + loopbackAddr + ":" + strconv.Itoa(hostPort) + path
}

// frameSources are the origins the page may frame: the browser tabs and
// docs URLs of every lab that still runs, never the server's own
// origin (a framed page there could read the session token).
func (s *Server) frameSources() []string {
	labs, err := RecordedLabs()
	if err != nil {
		return nil
	}
	self := "http://" + loopbackAddr + ":" + strconv.Itoa(s.port)
	set := map[string]bool{}
	for _, lab := range labs {
		rec, err := ReadRun(lab)
		if err != nil {
			continue
		}
		if current, err := CurrentRunDigest(lab); err != nil || current == "" || current != rec.RunID {
			continue
		}
		for _, t := range rec.tabs() {
			var origin string
			switch {
			case t.Kind == TabBrowser:
				origin, err = browserOrigin(t.URL)
			case t.Kind == TabDocs && t.URL != "":
				origin, err = config.TabDocsOrigin(t.URL)
			default:
				continue
			}
			if err == nil && origin != self {
				set[origin] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for o := range set {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// pagePolicy is the Content Security Policy of the page itself:
// ContentSecurityPolicy plus a frame-src naming exactly frameSources.
// Without any, frames stay limited to default-src 'self'.
func (s *Server) pagePolicy() string {
	sources := s.frameSources()
	if len(sources) == 0 {
		return ContentSecurityPolicy
	}
	return ContentSecurityPolicy + "; frame-src " + strings.Join(sources, " ")
}

// handleTabDoc is GET /runs/{lab}/tabs/{name}/doc: a docs tab's Markdown,
// rendered like the question. After Start only.
func (s *Server) handleTabDoc(w http.ResponseWriter, r *http.Request) {
	rec, ok := loadRun(w, r.PathValue("lab"))
	if !ok {
		return
	}
	t, found := rec.tab(r.PathValue("name"))
	if !found || t.Kind != TabDocs || t.URL != "" {
		writeError(w, http.StatusNotFound, "no such document")
		return
	}
	v, err := buildView(rec, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch v.Status {
	case StatusStopped:
		writeErrorCode(w, http.StatusConflict, "run_stopped", "the lab is no longer running")
		return
	case StatusReady:
		writeErrorCode(w, http.StatusConflict, "not_started", "click Start first")
		return
	}
	html, err := renderMarkdown(t.Doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": t.Name, "title": t.Title, "html": html})
}
