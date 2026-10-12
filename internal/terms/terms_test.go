package terms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), ".astrona")}
}

func TestConsent(t *testing.T) {
	for _, tc := range []struct {
		name, input                            string
		interactive, ci, explicit, saved, want bool
	}{
		{"first yes", "yes\n", true, false, false, false, true},
		{"first short yes", " Y \r\n", true, false, false, false, true},
		{"decline", "no\n", true, false, false, false, false},
		{"blank", "\n", true, false, false, false, false},
		{"EOF", "", true, false, false, false, false},
		{"incomplete yes", "yes", true, false, false, false, false},
		{"oversized", strings.Repeat("y", 40) + "\n", true, false, false, false, false},
		{"piped yes is not consent", "yes\n", false, false, false, false, false},
		{"flag", "", false, false, true, false, true},
		{"saved", "", true, false, false, true, true},
		{"headless saved", "", false, false, false, true, true},
		{"CI needs flag", "yes\n", true, true, false, false, false},
		{"CI ignores saved", "yes\n", true, true, false, true, false},
		{"CI flag", "", false, true, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			if tc.saved {
				if err := s.Accept("prompt", "test"); err != nil {
					t.Fatal(err)
				}
			}
			in := strings.NewReader(tc.input)
			var out bytes.Buffer
			err := s.Ensure(context.Background(), in, &out, tc.interactive, tc.ci, tc.explicit, "test")
			if (err == nil) != tc.want {
				t.Fatalf("error = %v, output = %q", err, out.String())
			}
			if !tc.interactive || tc.ci || tc.explicit || tc.saved {
				if in.Len() != len(tc.input) {
					t.Fatal("read input without needing a prompt")
				}
			}
			accepted, err := s.Accepted()
			if err != nil || accepted != (tc.want || tc.saved) {
				t.Fatalf("receipt = %v, %v", accepted, err)
			}
			if tc.want && !tc.saved && !tc.explicit && !strings.Contains(out.String(), Document()) {
				t.Fatal("prompt omitted terms")
			}
		})
	}
}

func TestConsentDoesNotConsumeNextPrompt(t *testing.T) {
	in := strings.NewReader("yes\nno\n")
	if !readConsent(in) {
		t.Fatal("yes was rejected")
	}
	rest, err := io.ReadAll(in)
	if err != nil || string(rest) != "no\n" {
		t.Fatalf("next answer = %q, %v", rest, err)
	}
}

func TestReceiptChangesAndCorruptionRequireNewConsent(t *testing.T) {
	for _, changed := range []string{"revision", "sha256", "acceptedAt", "method", "corrupt", "oversized", "null"} {
		t.Run(changed, func(t *testing.T) {
			s := testStore(t)
			if err := s.Accept("prompt", "old-cli"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Dir, "terms.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var receipt map[string]any
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			switch changed {
			case "corrupt":
				data = []byte("broken")
			case "oversized":
				data = append(data, bytes.Repeat([]byte(" "), 8193)...)
			case "null":
				data = []byte("null")
			default:
				receipt[changed] = "outdated"
				data, err = json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if accepted, err := s.Accepted(); err != nil || accepted {
				t.Fatalf("accepted = %v, %v", accepted, err)
			}
			if err := s.Ensure(context.Background(), strings.NewReader(""), io.Discard, false, false, false, "new-cli"); err == nil {
				t.Fatal("stale receipt accepted")
			}
			if err := s.Ensure(context.Background(), strings.NewReader("yes\n"), io.Discard, true, false, false, "new-cli"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReceiptPermissionsAndConcurrentWrites(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.Accept("flag", "test"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted, err := s.Accepted(); err != nil || !accepted {
		t.Fatalf("accepted = %v, %v", accepted, err)
	}
	for name, want := range map[string]os.FileMode{".": 0o700, "terms.json": 0o600} {
		info, err := os.Stat(filepath.Join(s.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s permissions = %o", name, info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("left temporary files: %v, %v", entries, err)
	}
}

func TestReceiptRefusesUnsafePaths(t *testing.T) {
	for _, kind := range []string{"directory symlink", "receipt symlink", "receipt directory"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			target := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "directory symlink" {
				err = os.Symlink(filepath.Dir(target), s.Dir)
			} else {
				if err := os.Mkdir(s.Dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "receipt symlink" {
					err = os.Symlink(target, filepath.Join(s.Dir, "terms.json"))
				} else {
					err = os.Mkdir(filepath.Join(s.Dir, "terms.json"), 0o700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Accepted(); err == nil {
				t.Fatal("unsafe read accepted")
			}
			if err := s.Accept("flag", "test"); err == nil {
				t.Fatal("unsafe write accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "original" {
				t.Fatalf("target changed: %s, %v", data, err)
			}
		})
	}
}

func TestGateCancellationAndWriteFailure(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Ensure(ctx, nil, io.Discard, false, false, true, "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if _, err := os.Stat(s.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled gate wrote data: %v", err)
	}
	if err := os.WriteFile(s.Dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure(context.Background(), nil, io.Discard, false, false, true, "test"); err == nil {
		t.Fatal("continued without a saved receipt")
	}
}

func TestCIDetection(t *testing.T) {
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "JENKINS_URL", "TF_BUILD", "CIRCLECI", "TEAMCITY_VERSION"} {
		for _, value := range []string{"true", "1", "https://jenkins.example", "false", "0", ""} {
			got := InCI(func(k string) string {
				if k == key {
					return value
				}
				return ""
			})
			want := value != "false" && value != "0" && value != ""
			if got != want {
				t.Errorf("%s=%s: CI=%v", key, value, got)
			}
		}
	}
}
