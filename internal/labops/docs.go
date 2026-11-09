package labops

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"astrona/internal/config"
)

// maxDocBytes bounds a lab doc fetched from a URL.
const maxDocBytes = 2 * 1024 * 1024

// LabDoc is one entry of metadata.docs as `astrona docs` exposes it.
type LabDoc struct {
	Key, Title, Path string
	Spoiler          bool // shown with a warning: it gives the solution away
}

// LabDocs lists the docs a lab's config names, in display order.
func LabDocs(cfg *config.LabConfig) []LabDoc {
	d := cfg.Metadata.Docs
	all := []LabDoc{
		{"question", "The task", d.Question, false},
		{"case-study", "Case study — the same task, with hints", d.CaseStudy, false},
		{"prerequisites", "Prerequisites — what to know first", d.Prerequisites, false},
		{"solution", "Solution — step by step", d.Solution, true},
	}
	var out []LabDoc
	for _, doc := range all {
		if doc.Path != "" {
			out = append(out, doc)
		}
	}
	return out
}

// ReadLabDoc reads a doc relative to the lab's config: within baseDir for
// a local/git config (JoinWithinBaseDir — a doc path must be
// relative and can't escape it), or
// resolved against the config's URL (https only, size-capped) for a remote
// config.
func ReadLabDoc(configPath, docPath string) (string, error) {
	if strings.HasPrefix(configPath, "http://") || strings.HasPrefix(configPath, "https://") {
		base, err := url.Parse(configPath)
		if err != nil {
			return "", fmt.Errorf("parse config URL: %w", err)
		}
		if strings.Contains(docPath, "://") || strings.HasPrefix(docPath, "/") {
			return "", fmt.Errorf("doc path '%s' must be relative to the config", docPath)
		}
		ref := *base
		ref.Path = path.Join(path.Dir(base.Path), docPath)
		if !strings.HasPrefix(ref.Path, path.Dir(base.Path)+"/") {
			return "", fmt.Errorf("doc path '%s' escapes the config's location", docPath)
		}
		tmp, cleanup, err := config.DownloadToTemp(ref.String(), "astrona-doc-*.md", maxDocBytes)
		if err != nil {
			return "", fmt.Errorf("fetch %s: %w", ref.String(), err)
		}
		defer cleanup()
		data, err := os.ReadFile(tmp)
		return string(data), err
	}

	full, err := config.JoinWithinBaseDir(filepath.Dir(configPath), docPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", docPath, err)
	}
	return string(data), nil
}

// DocFileFor is the usual file name of a doc key ("case-study" →
// case-study.md).
func DocFileFor(key string) string {
	if key == "case-study" {
		return config.DocFileNames["caseStudy"]
	}
	return config.DocFileNames[key]
}
