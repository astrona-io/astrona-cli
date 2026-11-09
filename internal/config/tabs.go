package config

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Tab types: what a tab of the browser workspace shows.
const (
	TabTerminal = "terminal"
	TabBrowser  = "browser"
	TabDocs     = "docs"
)

// FocusTabName is the name of the browser workspace's first tab: the lab's
// main terminal. It is always there and cannot be declared.
const FocusTabName = "main"

// MaxTabs bounds `tabs:`. The focus tab comes on top.
const MaxTabs = 8

// MaxTabDocBytes bounds a Markdown file a docs tab shows.
const MaxTabDocBytes = 64 << 10

// Tab is one entry of `tabs:`: an extra tab in the browser workspace, next
// to the focus tab (the lab's main terminal). The learner can switch tabs
// but never add or close one; the lab decides what they are.
//
//   - terminal: another isolated terminal, on the lab's own cluster or on
//     a linked cluster (Cluster, a runtime.kind.clusters name).
//   - browser: an app the lab serves through one of its runtime.portForwards
//     (PortForward, scheme http or https), shown in the page. Only the
//     forward's own 127.0.0.1 address is ever framed.
//   - docs: documentation the learner may read during the lab, either a
//     Markdown file shipped with the lab (File, relative to config.yaml) or
//     an https URL, framed in the tab (a site that refuses framing stays
//     blank there; the tab can open it in its own window).
//
// Without `tabs:` the workspace shows only the focus tab; a linked cluster
// gets a terminal tab only when the lab lists it.
type Tab struct {
	Name        string `yaml:"name"`
	Title       string `yaml:"title"`
	Type        string `yaml:"type"`
	Cluster     string `yaml:"cluster"`
	PortForward string `yaml:"portForward"`
	Path        string `yaml:"path"`
	URL         string `yaml:"url"`
	File        string `yaml:"file"`
}

// TabTypes are the values of tabs[].type.
var TabTypes = []string{TabTerminal, TabBrowser, TabDocs}

// tabHost is the host part of a docs URL: a plain DNS name, so the origin
// can go into the workspace's Content Security Policy as it is.
var tabHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// TabDocsOrigin is the origin of a docs tab's URL ("https://host[:port]"),
// or an error when the URL is not a plain https URL.
func TabDocsOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil {
		return "", fmt.Errorf("url %q must be an https:// URL", raw)
	}
	host := u.Hostname()
	if !tabHost.MatchString(host) {
		return "", fmt.Errorf("url %q must name a host like docs.example.com", raw)
	}
	origin := "https://" + host
	if p := u.Port(); p != "" {
		origin += ":" + p
	}
	return origin, nil
}

// TabPath checks a browser tab's path: empty or an absolute path with no
// scheme, host, backslash or control character. It returns the path to use.
func TabPath(p string) (string, error) {
	if p == "" {
		return "/", nil
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\ \t\r\n#") {
		return "", fmt.Errorf("path %q must start with a single '/' and contain no spaces, '\\' or '#'", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("path %q contains a control character", p)
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", fmt.Errorf("path %q is not a plain path", p)
	}
	return p, nil
}

// ValidateTabs checks `tabs:`: kind labs only, unique names, a known type
// and what each type needs. Whether a docs file exists is checked by
// `astrona validate`, which knows the lab's folder.
func ValidateTabs(cfg *LabConfig) error {
	if len(cfg.Tabs) == 0 {
		return nil
	}
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return fmt.Errorf("tabs are only supported for the kind runtime (this lab uses '%s')", cfg.Runtime.Type)
	}
	if len(cfg.Tabs) > MaxTabs {
		return fmt.Errorf("tabs: at most %d tabs (the main terminal comes on top)", MaxTabs)
	}
	clusters := map[string]bool{}
	for _, c := range cfg.KindClusters() {
		clusters[c.Name] = true
	}
	forwards := map[string]PortForward{}
	for _, pf := range cfg.Runtime.PortForwards {
		forwards[pf.Name] = pf.Normalized()
	}
	seen := map[string]bool{FocusTabName: true}
	for i, t := range cfg.Tabs {
		where := fmt.Sprintf("tabs[%d]", i)
		if !dns1123Label.MatchString(t.Name) {
			return fmt.Errorf("%s: name '%s' must be a lowercase DNS label (a-z, 0-9, '-', max 63 chars)", where, t.Name)
		}
		if t.Name == FocusTabName {
			return fmt.Errorf("%s: name '%s' is the lab's main terminal, which is always the first tab", where, t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("%s: tab name '%s' is used twice", where, t.Name)
		}
		seen[t.Name] = true
		title := strings.TrimSpace(t.Title)
		if title == "" {
			return fmt.Errorf("%s: title is required (what the tab shows)", where)
		}
		if len(title) > 40 || strings.ContainsFunc(title, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("%s: title must be one line of at most 40 characters", where)
		}
		set := func(field, value string) error {
			if value != "" {
				return fmt.Errorf("%s: %s does not apply to a %s tab", where, field, t.Type)
			}
			return nil
		}
		var err error
		switch t.Type {
		case TabTerminal:
			for _, f := range [][2]string{{"portForward", t.PortForward}, {"path", t.Path}, {"url", t.URL}, {"file", t.File}} {
				if err = set(f[0], f[1]); err != nil {
					return err
				}
			}
			if t.Cluster != "" && !clusters[t.Cluster] {
				return fmt.Errorf("%s: cluster '%s' is not one of runtime.kind.clusters (leave it out for the lab's own cluster)", where, t.Cluster)
			}
		case TabBrowser:
			for _, f := range [][2]string{{"cluster", t.Cluster}, {"url", t.URL}, {"file", t.File}} {
				if err = set(f[0], f[1]); err != nil {
					return err
				}
			}
			pf, ok := forwards[t.PortForward]
			if !ok {
				return fmt.Errorf("%s: portForward '%s' is not one of runtime.portForwards", where, t.PortForward)
			}
			if pf.Scheme != "http" && pf.Scheme != "https" {
				return fmt.Errorf("%s: port forward '%s' must have scheme http or https to show in a browser tab", where, pf.Name)
			}
			if _, err = TabPath(t.Path); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
		case TabDocs:
			for _, f := range [][2]string{{"cluster", t.Cluster}, {"portForward", t.PortForward}, {"path", t.Path}} {
				if err = set(f[0], f[1]); err != nil {
					return err
				}
			}
			if (t.URL == "") == (t.File == "") {
				return fmt.Errorf("%s: a docs tab needs either url (https) or file (Markdown in the lab), not both", where)
			}
			if t.URL != "" {
				if _, err = TabDocsOrigin(t.URL); err != nil {
					return fmt.Errorf("%s: %w", where, err)
				}
				break
			}
			if err = validateTabFile(cfg, t.File); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
		default:
			return fmt.Errorf("%s: unsupported type '%s' (%s)", where, t.Type, strings.Join(TabTypes, ", "))
		}
	}
	return nil
}

// validateTabFile refuses a docs file that is not a relative Markdown path
// in the lab, or that is the lab's solution.
func validateTabFile(cfg *LabConfig, file string) error {
	clean := path.Clean(file)
	if path.IsAbs(file) || strings.Contains(file, `\`) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("file %q must be a path relative to config.yaml, inside the lab", file)
	}
	if ext := strings.ToLower(path.Ext(clean)); ext != ".md" && ext != ".markdown" {
		return fmt.Errorf("file %q must be a Markdown file (.md)", file)
	}
	for _, secret := range []string{cfg.Metadata.Docs.Solution, cfg.Metadata.Docs.Guide, DocFileNames["solution"]} {
		if secret != "" && strings.EqualFold(clean, path.Clean(secret)) {
			return fmt.Errorf("file %q is the lab's solution, which is never shown during the lab", file)
		}
	}
	return nil
}
