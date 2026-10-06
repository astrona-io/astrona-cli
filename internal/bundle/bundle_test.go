package bundle

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, p, content string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0700)
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func makeBundle(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	lab := filepath.Join(dir, "lab")
	writeFile(t, filepath.Join(lab, "config.yaml"), "metadata: {name: x}\n")
	writeFile(t, filepath.Join(lab, "docs", "q.md"), "# Q\n")
	writeFile(t, filepath.Join(lab, ".git", "HEAD"), "ref: x\n")
	img := writeFile(t, filepath.Join(dir, "img.tar"), "fake image archive")
	sum, _ := SHA256File(img)

	entries, err := LabEntries(lab)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{FormatVersion: FormatVersion, Lab: "x", Arch: "arm64", CreatedAt: time.Now(),
		Images: []Image{{Ref: "nginx:1.27", File: "images/0.tar", SHA256: sum, Purpose: "preload"}}}
	out := filepath.Join(dir, "x.tar.gz")
	if err := Write(out, m, append(entries, Entry{Name: "images/0.tar", Path: img})); err != nil {
		t.Fatal(err)
	}
	return out, dir
}

func TestWriteExtractRoundTrip(t *testing.T) {
	out, _ := makeBundle(t)
	dest := filepath.Join(t.TempDir(), "x")
	m, err := Extract(out, dest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Lab != "x" || len(m.Images) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "lab", "docs", "q.md")); string(data) != "# Q\n" {
		t.Error("lab file not extracted")
	}
	if _, err := os.Stat(filepath.Join(dest, "lab", ".git")); !os.IsNotExist(err) {
		t.Error(".git must not be bundled")
	}
}

// rawBundle writes a tar.gz with arbitrary headers, for hostile cases.
func rawBundle(t *testing.T, hdrs ...*tar.Header) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range hdrs {
		if h.Typeflag == tar.TypeReg || h.Typeflag == 0 {
			h.Size = 1
		}
		tw.WriteHeader(h)
		if h.Size > 0 {
			tw.Write([]byte("x"))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func TestExtractRejectsHostileArchives(t *testing.T) {
	cases := map[string]*tar.Header{
		"parent escape":   {Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0644},
		"nested escape":   {Name: "lab/../../escape.txt", Typeflag: tar.TypeReg, Mode: 0644},
		"absolute path":   {Name: "/tmp/escape.txt", Typeflag: tar.TypeReg, Mode: 0644},
		"symlink":         {Name: "lab/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"hard link":       {Name: "lab/hard", Typeflag: tar.TypeLink, Linkname: "lab/config.yaml"},
		"backslash trick": {Name: `lab\..\..\x`, Typeflag: tar.TypeReg, Mode: 0644},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "dest")
			if _, err := Extract(rawBundle(t, h), dest); err == nil {
				t.Fatal("hostile entry accepted")
			}
			if _, err := os.Stat(filepath.Join(parent, "escape.txt")); err == nil {
				t.Fatal("file written outside the destination")
			}
		})
	}
}

func TestExtractVerifiesChecksumsAndFormat(t *testing.T) {
	out, _ := makeBundle(t)
	dest := filepath.Join(t.TempDir(), "x")
	if _, err := Extract(out, dest); err != nil {
		t.Fatal(err)
	}

	// Rebuild with a manifest whose image hash doesn't match the file.
	dir := t.TempDir()
	img := writeFile(t, filepath.Join(dir, "img.tar"), "tampered")
	m := Manifest{FormatVersion: FormatVersion, Lab: "x", Images: []Image{{Ref: "a", File: "images/0.tar", SHA256: strings.Repeat("0", 64)}}}
	bad := filepath.Join(dir, "bad.tar.gz")
	Write(bad, m, []Entry{{Name: "images/0.tar", Path: img}})
	if _, err := Extract(bad, filepath.Join(dir, "out")); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered image = %v", err)
	}

	future := filepath.Join(dir, "future.tar.gz")
	Write(future, Manifest{FormatVersion: 99, Lab: "x"}, nil)
	if _, err := Extract(future, filepath.Join(dir, "out2")); err == nil || !strings.Contains(err.Error(), "format 99") {
		t.Fatalf("future format = %v", err)
	}

	if _, err := Extract(img, filepath.Join(dir, "out3")); err == nil {
		t.Fatal("non-gzip accepted")
	}
}

func TestExtractRejectsUnsafeLabName(t *testing.T) {
	dir := t.TempDir()
	for i, lab := range []string{"../../escape", "a/b", "..", "a,b"} {
		out := filepath.Join(dir, "b.tar.gz")
		if err := Write(out, Manifest{FormatVersion: FormatVersion, Lab: lab}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := Extract(out, filepath.Join(dir, "out", string(rune('a'+i)))); err == nil || !strings.Contains(err.Error(), "lab") {
			t.Errorf("lab %q: Extract = %v, want lab name error", lab, err)
		}
	}
	// A lab without metadata.name still loads.
	out := filepath.Join(dir, "empty.tar.gz")
	if err := Write(out, Manifest{FormatVersion: FormatVersion}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(out, filepath.Join(dir, "out", "empty")); err != nil {
		t.Errorf("empty lab: %v", err)
	}
}

func TestLabEntriesRefusesSymlinks(t *testing.T) {
	lab := t.TempDir()
	writeFile(t, filepath.Join(lab, "config.yaml"), "x")
	os.Symlink("/etc/passwd", filepath.Join(lab, "leak"))
	if _, err := LabEntries(lab); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink in lab = %v", err)
	}
}
