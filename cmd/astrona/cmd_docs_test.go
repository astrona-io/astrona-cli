package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestSanitizeTerminalText(t *testing.T) {
	in := "title\x1b]0;pwned\x07 ok\x1b[2J\r\nline2\tx\x9bmore\x00"
	got := sanitizeTerminalText(in)
	if strings.ContainsAny(got, "\x1b\x07\x00\r\u009b") {
		t.Fatalf("control characters survived: %q", got)
	}
	if got != "title]0;pwned ok[2J\nline2\txmore" {
		t.Fatalf("sanitize = %q", got)
	}
}

func TestRenderMarkdown(t *testing.T) {
	src := "# Title\n## Sub\nSome `code` and **bold** and [link](https://x.y).\n- item\n> quote\n---\n```\nkubectl get pods\n```\n"

	if got := renderMarkdown(src, false); got != src {
		t.Fatalf("no-color must return the source unchanged:\n%q", got)
	}

	got := renderMarkdown(src, true)
	for _, want := range []string{
		ansiBold + "\x1b[4mTitle" + ansiReset,
		ansiBold + "Sub" + ansiReset,
		ansiCyan + "code" + ansiReset,
		ansiBold + "bold" + ansiReset,
		"link \x1b[2m(https://x.y)",
		"• item",
		"│ quote",
		"────",
		"    " + ansiCyan + "kubectl get pods" + ansiReset,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "```") {
		t.Error("code fences should not be printed")
	}
}

func TestReadLabDocStaysInsideLab(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0700)
	os.WriteFile(filepath.Join(dir, "docs", "q.md"), []byte("# Q\n"), 0600)
	cfgPath := filepath.Join(dir, "config.yaml")

	if got, err := readLabDoc(cfgPath, "docs/q.md"); err != nil || got != "# Q\n" {
		t.Fatalf("readLabDoc = %q, %v", got, err)
	}
	if _, err := readLabDoc(cfgPath, "../../etc/passwd"); err == nil {
		t.Fatal("doc path escaping the lab dir was read")
	}

	for _, bad := range []string{"../secret.md", "/etc/passwd", "https://evil.example/x.md"} {
		if _, err := readLabDoc("https://labs.example/lab/config.yaml", bad); err == nil {
			t.Errorf("remote doc path %q accepted", bad)
		}
	}
}

func TestLabDocsAndList(t *testing.T) {
	cfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: "x", Docs: config.DocsConfig{ExamQuestion: "q.md", Guide: "g.md"}}}
	docs := labDocs(cfg)
	if len(docs) != 2 || docs[0].key != "question" || docs[1].key != "guide" || !docs[1].spoiler || docs[0].spoiler {
		t.Fatalf("labDocs = %+v", docs)
	}

	var buf bytes.Buffer
	printDocList(&buf, "x", docs)
	if !strings.Contains(buf.String(), "astrona docs question") || strings.Contains(buf.String(), "case-study") {
		t.Errorf("list = %q", buf.String())
	}
	buf.Reset()
	printDocList(&buf, "x", nil)
	if !strings.Contains(buf.String(), "has no docs") {
		t.Errorf("empty list = %q", buf.String())
	}

	if configFlagHint(".") != "" || configFlagHint("labs/x") != " -c labs/x" {
		t.Error("configFlagHint wrong")
	}
}

func TestSplitDocsArgs(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		key, lab string
		wantErr  bool
	}{
		{nil, "", "", false},
		{[]string{"question"}, "question", "", false},
		{[]string{"ATS016/section-020/module-01/lab-01"}, "", "ATS016/section-020/module-01/lab-01", false},
		{[]string{"question", "ATS016/section-020/module-01/lab-01"}, "question", "ATS016/section-020/module-01/lab-01", false},
		{[]string{"ATS016/section-020/module-01/lab-01", "guide"}, "guide", "ATS016/section-020/module-01/lab-01", false},
		{[]string{"answers"}, "", "", true},
		{[]string{"question", "guide"}, "", "", true},
	} {
		key, lab, err := splitDocsArgs(tc.args)
		if (err != nil) != tc.wantErr || key != tc.key || lab != tc.lab {
			t.Errorf("splitDocsArgs(%q) = %q, %q, %v", tc.args, key, lab, err)
		}
	}
}
