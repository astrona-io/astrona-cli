package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// path.yaml often comes from a cloned remote repo: stage IDs and refs become
// directory names under the build output, and content paths are read from
// the referenced repo, so none of them may point outside those directories.
func TestLoadTrainingPathValidatesPaths(t *testing.T) {
	cases := []struct {
		name, stageID, ref, path string
		wantErr                  string // "" = valid
	}{
		{"valid", "ATP001-STG001", "ATS002", ".", ""},
		{"valid subdir path", "stg_1.v2", "ats-002", "labs/one", ""},
		{"empty path", "STG1", "ATS1", "", ""},
		{"absolute stage id", "/Users/victim", "ATS1", ".", "spec.stages[0].id"},
		{"traversal stage id", "..", "ATS1", ".", "spec.stages[0].id"},
		{"dotdot inside stage id", "a..b", "ATS1", ".", "spec.stages[0].id"},
		{"slash in stage id", "a/b", "ATS1", ".", "spec.stages[0].id"},
		{"dot stage id", ".", "ATS1", ".", "spec.stages[0].id"},
		{"empty stage id", "", "ATS1", ".", "spec.stages[0].id"},
		{"absolute ref", "STG1", "/etc", ".", "content[0].ref"},
		{"traversal ref", "STG1", "../../x", ".", "content[0].ref"},
		{"empty ref", "STG1", "", ".", "content[0].ref"},
		{"absolute path", "STG1", "ATS1", "/Users/victim/.ssh", "content[0].path"},
		{"traversal path", "STG1", "ATS1", "labs/../../x", "content[0].path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			yaml := `apiVersion: content.astrona.io/v1alpha1
kind: TrainingPath
spec:
  stages:
    - id: "` + c.stageID + `"
      content:
        - ref: "` + c.ref + `"
          repository: "https://example.com/x.git"
          path: "` + c.path + `"
`
			p := filepath.Join(t.TempDir(), "path.yaml")
			if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadTrainingPath(p)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want one mentioning %q", err, c.wantErr)
			}
		})
	}
}
