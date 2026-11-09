package authoring

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitContentWithGitLocalRepo(t *testing.T) {
	// Skip if git is not available in PATH
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found in PATH, skipping integration cloning test")
	}

	// 1. Create a mock template directory on disk
	tempTemplateRepoDir := t.TempDir()

	files := []struct {
		relPath string
		content string
	}{
		{
			relPath: "config.yaml",
			content: `metadata:
  path_id: "{{path_id}}"
  slug: "{{.Slug}}"
  title: "{{title}}"
  description: "{{description}}"
  version: "{{.Version}}"
  language: "{{language}}"
  author: "{{.AuthorName}}"
  license: "{{license}}"
`,
		},
		{
			relPath: "README.md",
			content: `# {{title}}

This is the repository for training path {{path_id}}.
Managed by {{author_name}} with license {{.License}}.
`,
		},
		{
			relPath: "docs/{{slug}}-guide.md",
			content: `Welcome to the guide for {{slug}}!`,
		},
	}

	for _, f := range files {
		fullPath := filepath.Join(tempTemplateRepoDir, f.relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create directory for %s: %v", f.relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(f.content), 0644); err != nil {
			t.Fatalf("failed to write mock template file %s: %v", f.relPath, err)
		}
	}

	// 2. Initialize local git repository in template directory
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = tempTemplateRepoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to run %s %v: %v\nOutput: %s", name, args, err, string(out))
		}
	}

	runCmd(gitPath, "init")
	runCmd(gitPath, "config", "user.name", "test-user")
	runCmd(gitPath, "config", "user.email", "test@test.com")
	runCmd(gitPath, "config", "commit.gpgsign", "false")
	runCmd(gitPath, "add", ".")
	runCmd(gitPath, "commit", "-m", "initial commit")

	// 3. Setup a target directory where we want to scaffold the new path
	targetDir := filepath.Join(t.TempDir(), "my-custom-path")

	// 4. Run 'content init atp <path>' pointing to our mock git repository as the blueprint
	opts := ContentInitOptions{
		ContentType:  "atp",
		Path:         targetDir,
		Repo:         tempTemplateRepoDir,
		PathID:       "ATP999",
		Slug:         "my-legendary-slug",
		Title:        "My Legendary Title",
		Description:  "This is a custom test path description.",
		Version:      "2.4.6",
		Language:     "fr",
		AuthorName:   "Jean-Pierre",
		License:      "MIT",
		Codeowners:   "@alice @bob @my-org/team-alpha",
		GitHubOrg:    "astrona-io",
		GitHubPublic: true,
		Stdout:       io.Discard,
		Stderr:       io.Discard,
	}

	// Execute
	if err := InitContent(opts); err != nil {
		t.Fatalf("expected content init execution to succeed, got: %v", err)
	}

	// 5. Assert files were successfully copied and templated!
	expectedFiles := []struct {
		relPath      string
		expectedSubs []string
	}{
		{
			relPath: "config.yaml",
			expectedSubs: []string{
				`path_id: "ATP999"`,
				`slug: "my-legendary-slug"`,
				`title: "My Legendary Title"`,
				`description: "This is a custom test path description."`,
				`version: "2.4.6"`,
				`language: "fr"`,
				`author: "Jean-Pierre"`,
				`license: "MIT"`,
			},
		},
		{
			relPath: "README.md",
			expectedSubs: []string{
				`# My Legendary Title`,
				`This is the repository for training path ATP999.`,
				`Managed by Jean-Pierre with license MIT.`,
			},
		},
		{
			relPath: "docs/my-legendary-slug-guide.md", // Checks name replacement
			expectedSubs: []string{
				`Welcome to the guide for my-legendary-slug!`,
			},
		},
	}

	for _, ef := range expectedFiles {
		fullPath := filepath.Join(targetDir, ef.relPath)
		_, err := os.Stat(fullPath)
		if err != nil {
			t.Errorf("expected file %s to be created, but got: %v", ef.relPath, err)
			continue
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("failed to read file %s: %v", ef.relPath, err)
			continue
		}
		content := string(data)

		for _, sub := range ef.expectedSubs {
			if !strings.Contains(content, sub) {
				t.Errorf("expected file %s to contain %q, but did not.\nContent:\n%s", ef.relPath, sub, content)
			}
		}
	}

	// Verify that CODEOWNERS was successfully generated under targetDir/.github/CODEOWNERS
	codeownersFile := filepath.Join(targetDir, ".github", "CODEOWNERS")
	if _, err := os.Stat(codeownersFile); err != nil {
		t.Errorf("expected CODEOWNERS file to be created, but got: %v", err)
	} else {
		data, err := os.ReadFile(codeownersFile)
		if err != nil {
			t.Errorf("failed to read CODEOWNERS file: %v", err)
		} else {
			content := string(data)
			if !strings.Contains(content, "CODEOWNERS for ATP999") {
				t.Errorf("expected CODEOWNERS file to contain 'CODEOWNERS for ATP999', got: %s", content)
			}
			if !strings.Contains(content, "@alice @bob @my-org/team-alpha") {
				t.Errorf("expected CODEOWNERS file to contain '@alice @bob @my-org/team-alpha', got: %s", content)
			}
		}
	}

	// Verify that the template's .git folder was NOT copied
	gitFolder := filepath.Join(targetDir, ".git")
	if _, err := os.Stat(gitFolder); !os.IsNotExist(err) {
		t.Error("expected .git directory from template repository NOT to be copied, but it exists in target folder")
	}

	// Verify pre-flight check error if target directory already has files
	if err := InitContent(opts); err == nil {
		t.Error("expected pre-flight error on existing path, but got nil")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected error message to contain 'already exists', got: %v", err)
	}
}

func TestInitContentCodeownersDisabled(t *testing.T) {
	// Skip if git is not available in PATH
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found in PATH, skipping integration cloning test")
	}

	tempTemplateRepoDir := t.TempDir()

	// Create README.md
	if err := os.WriteFile(filepath.Join(tempTemplateRepoDir, "README.md"), []byte("# My Template"), 0644); err != nil {
		t.Fatalf("failed to write README: %v", err)
	}

	// Git init
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = tempTemplateRepoDir
		_ = cmd.Run()
	}
	runCmd(gitPath, "init")
	runCmd(gitPath, "config", "user.name", "test-user")
	runCmd(gitPath, "config", "user.email", "test@test.com")
	runCmd(gitPath, "config", "commit.gpgsign", "false")
	runCmd(gitPath, "add", ".")
	runCmd(gitPath, "commit", "-m", "initial commit")

	targetDir := filepath.Join(t.TempDir(), "my-custom-path-disabled-codeowners")

	// Codeowners "false" (--codeowners=false) disables CODEOWNERS
	opts := ContentInitOptions{
		ContentType:  "atp",
		Path:         targetDir,
		Repo:         tempTemplateRepoDir,
		Codeowners:   "false",
		GitHubOrg:    "astrona-io",
		GitHubPublic: true,
		Stdout:       io.Discard,
		Stderr:       io.Discard,
	}

	// Execute
	if err := InitContent(opts); err != nil {
		t.Fatalf("expected content init execution to succeed, got: %v", err)
	}

	// Verify CODEOWNERS file does NOT exist
	codeownersFile := filepath.Join(targetDir, ".github", "CODEOWNERS")
	if _, err := os.Stat(codeownersFile); !os.IsNotExist(err) {
		t.Error("expected CODEOWNERS file NOT to be created when --codeowners=false, but it exists")
	}
}
