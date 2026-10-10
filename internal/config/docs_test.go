package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadDocs(t *testing.T, yml string, files ...string) (DocsConfig, error) {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadLabConfig(p)
	if err != nil {
		return DocsConfig{}, err
	}
	if len(cfg.UnknownFields) > 0 {
		t.Errorf("unknown fields: %+v", cfg.UnknownFields)
	}
	return cfg.Metadata.Docs, nil
}

func TestDocsNames(t *testing.T) {
	// The new format (question/solution), as ATS015 writes it.
	d, err := loadDocs(t, "metadata:\n  name: lab\n  docs:\n    question: q.md\n    solution: s.md\n")
	if err != nil || d.Question != "q.md" || d.Solution != "s.md" {
		t.Errorf("new names = %+v, %v", d, err)
	}
	// The older names still work and are folded into the new ones.
	d, err = loadDocs(t, "metadata:\n  name: lab\n  docs:\n    examQuestion: q.md\n    guide: s.md\n")
	if err != nil || d.Question != "q.md" || d.Solution != "s.md" || d.ExamQuestion != "" || d.Guide != "" {
		t.Errorf("old names = %+v, %v", d, err)
	}
	// Both names of one doc, pointing at different files.
	if _, err := loadDocs(t, "metadata:\n  name: lab\n  docs:\n    question: a.md\n    examQuestion: b.md\n"); err == nil || !strings.Contains(err.Error(), "keep question") {
		t.Errorf("conflict = %v", err)
	}
	if _, err := loadDocs(t, "metadata:\n  name: lab\n  docs:\n    solution: a.md\n    guide: a.md\n"); err != nil {
		t.Errorf("same file under both names = %v", err)
	}
}

func TestDocsByFileName(t *testing.T) {
	// No docs block: found next to config.yaml.
	d, err := loadDocs(t, "metadata:\n  name: lab\n", "question.md", "solution.md", "case-study.md")
	if err != nil || d.Question != "question.md" || d.Solution != "solution.md" || d.CaseStudy != "case-study.md" || d.Prerequisites != "" {
		t.Errorf("by file name = %+v, %v", d, err)
	}
	// What the block lists wins; the rest is still found.
	d, err = loadDocs(t, "metadata:\n  name: lab\n  docs:\n    question: docs/task.md\n", "question.md", "solution.md")
	if err != nil || d.Question != "docs/task.md" || d.Solution != "solution.md" {
		t.Errorf("listed + found = %+v, %v", d, err)
	}
	// Nothing there, nothing listed.
	if d, err = loadDocs(t, "metadata:\n  name: lab\n"); err != nil || d != (DocsConfig{}) {
		t.Errorf("none = %+v, %v", d, err)
	}
}
