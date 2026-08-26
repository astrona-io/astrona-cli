package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewContentCmd(t *testing.T) {
	cmd := newContentCmd()

	if cmd.Use != "content" {
		t.Errorf("expected command Use 'content', got %q", cmd.Use)
	}

	subcmds := cmd.Commands()
	if len(subcmds) != 3 {
		t.Fatalf("expected 3 subcommands, got %d", len(subcmds))
	}

	initCmd := cmd.Commands()[0]
	for _, c := range subcmds {
		if strings.HasPrefix(c.Use, "init ") {
			initCmd = c
		}
	}
	if initCmd.Use != "init <type> [path]" {
		t.Errorf("expected subcommand Use 'init <type> [path]', got %q", initCmd.Use)
	}

	var validateCmd *cobra.Command
	for _, c := range subcmds {
		if strings.HasPrefix(c.Use, "validate ") {
			validateCmd = c
		}
	}
	if validateCmd == nil {
		t.Fatal("expected a 'validate' subcommand to be registered")
	}
	if validateCmd.Use != "validate <type> <source>" {
		t.Errorf("expected subcommand Use 'validate <type> <source>', got %q", validateCmd.Use)
	}
	if flg := validateCmd.Flag("git-ref"); flg == nil {
		t.Error("expected flag \"git-ref\" to be defined")
	} else if flg.Value.Type() != "string" {
		t.Errorf("expected flag \"git-ref\" to be type string, got %s", flg.Value.Type())
	}

	var buildCmd *cobra.Command
	for _, c := range subcmds {
		if strings.HasPrefix(c.Use, "build ") {
			buildCmd = c
		}
	}
	if buildCmd == nil {
		t.Fatal("expected a 'build' subcommand to be registered")
	}
	if buildCmd.Use != "build <type> <source> [flags]" {
		t.Errorf("expected subcommand Use 'build <type> <source> [flags]', got %q", buildCmd.Use)
	}
	for _, ef := range []struct {
		name string
		typ  string
	}{
		{"git-ref", "string"},
		{"output", "string"},
		{"clean", "bool"},
	} {
		flg := buildCmd.Flag(ef.name)
		if flg == nil {
			t.Errorf("expected flag %q to be defined", ef.name)
		} else if flg.Value.Type() != ef.typ {
			t.Errorf("expected flag %q to be type %s, got %s", ef.name, ef.typ, flg.Value.Type())
		}
	}

	expectedFlags := []struct {
		name string
		typ  string
	}{
		{"path-id", "string"},
		{"slug", "string"},
		{"title", "string"},
		{"description", "string"},
		{"version", "string"},
		{"language", "string"},
		{"author-name", "string"},
		{"license", "string"},
		{"repo", "string"},
		{"create-repo", "bool"},
		{"github-org", "string"},
		{"github-repo", "string"},
		{"github-public", "bool"},
		{"codeowners", "string"},
	}

	for _, ef := range expectedFlags {
		flg := initCmd.Flag(ef.name)
		if flg == nil {
			t.Errorf("expected flag %q to be defined", ef.name)
		} else if flg.Value.Type() != ef.typ {
			t.Errorf("expected flag %q to be type %s, got %s", ef.name, ef.typ, flg.Value.Type())
		}
	}
}

func TestContentInitCmdWithGitLocalRepo(t *testing.T) {
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

	// 4. Run 'content init atp <path>' pointing to our mock git repository as --repo
	initCmd := newContentInitCmd()
	initCmd.SetArgs([]string{"atp", targetDir})

	// Inject custom flags
	flagsToSet := map[string]string{
		"path-id":     "ATP999",
		"slug":        "my-legendary-slug",
		"title":       "My Legendary Title",
		"description": "This is a custom test path description.",
		"version":     "2.4.6",
		"language":    "fr",
		"author-name": "Jean-Pierre",
		"license":     "MIT",
		"repo":        tempTemplateRepoDir,
		"codeowners":  "@alice @bob @my-org/team-alpha",
	}

	for name, val := range flagsToSet {
		if err := initCmd.Flags().Set(name, val); err != nil {
			t.Fatalf("failed to set flag %s: %v", name, err)
		}
	}

	// Execute
	if err := initCmd.Execute(); err != nil {
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
	errCmd := newContentInitCmd()
	errCmd.SetArgs([]string{"atp", targetDir})
	if err := errCmd.Flags().Set("repo", tempTemplateRepoDir); err != nil {
		t.Fatalf("failed to set repo flag: %v", err)
	}
	if err := errCmd.Execute(); err == nil {
		t.Error("expected pre-flight error on existing path, but got nil")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected error message to contain 'already exists', got: %v", err)
	}
}

func TestContentInitCmdCodeownersDisabled(t *testing.T) {
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

	initCmd := newContentInitCmd()
	initCmd.SetArgs([]string{"atp", targetDir})

	// Inject flags including --codeowners=false
	if err := initCmd.Flags().Set("repo", tempTemplateRepoDir); err != nil {
		t.Fatalf("failed to set repo flag: %v", err)
	}
	if err := initCmd.Flags().Set("codeowners", "false"); err != nil {
		t.Fatalf("failed to set codeowners flag: %v", err)
	}

	// Execute
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("expected content init execution to succeed, got: %v", err)
	}

	// Verify CODEOWNERS file does NOT exist
	codeownersFile := filepath.Join(targetDir, ".github", "CODEOWNERS")
	if _, err := os.Stat(codeownersFile); !os.IsNotExist(err) {
		t.Error("expected CODEOWNERS file NOT to be created when --codeowners=false, but it exists")
	}
}

func TestContentInitCmdInvalidType(t *testing.T) {
	initCmd := newContentInitCmd()
	initCmd.SetArgs([]string{"invalid", "some-path"})

	if err := initCmd.Execute(); err == nil {
		t.Error("expected error when running init with invalid type, but got nil")
	} else if !strings.Contains(err.Error(), "invalid content type") {
		t.Errorf("expected error message to contain 'invalid content type', but got: %v", err)
	}
}
