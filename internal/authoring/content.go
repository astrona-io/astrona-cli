package authoring

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"astrona/internal/config"
	"astrona/internal/content"
	"astrona/internal/gitsource"
	"astrona/internal/ui"
)

// Default blueprint repositories `astrona content init` clones when --repo
// is not given.
const (
	atsBlueprint = "git@github.com:astrona-io/training-sandbox-blueprint.git"
	atpBlueprint = "git@github.com:astrona-io/training-path-blueprint.git"
)

// IsGitSource reports whether source names a git remote rather than a local
// folder path — the same set of transports gitsource/git itself accepts
// (https://, git@host:, ssh://), plus a bare ".git" suffix for other
// transports (e.g. git://, file://). A local path is never mistaken for a
// remote just because it contains "@" or ":" elsewhere.
func IsGitSource(source string) bool {
	return strings.HasPrefix(source, "https://") ||
		strings.HasPrefix(source, "http://") ||
		strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "ssh://") ||
		strings.HasSuffix(source, ".git")
}

// ResolveContentSource resolves source to a local directory: a git URL is
// cloned/updated into the gitsource cache (pinned to gitRef if set), a local
// path is returned as-is (absolute). Shared by `content validate` and
// `content build` since both start from the same `<type> <source>` shape.
func ResolveContentSource(source, gitRef string) (string, error) {
	if IsGitSource(source) {
		repoDir, err := gitsource.ResolveGitConfigSource(source, gitRef, true)
		if err != nil {
			return "", fmt.Errorf("failed to resolve git source %s: %w", source, err)
		}
		return repoDir, nil
	}

	if gitRef != "" {
		return "", fmt.Errorf("--git-ref only applies to a git source, not a local folder path")
	}
	absPath, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path %s: %w", source, err)
	}
	return absPath, nil
}

// ValidateATS writes what content.ValidateATS found in dir to w and fails
// when anything would keep the training, or one of its items, off
// astrona.io.
func ValidateATS(w io.Writer, dir string) error {
	report, err := content.ValidateATS(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s: %d readings, %d labs, %d playgrounds, %d quizzes (%d questions)\n",
		report.TrainingID, report.Readings, report.Labs, report.Playgrounds, report.Quizzes, report.Questions)
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "  ! %s\n", warning)
	}
	for _, problem := range report.Errors {
		fmt.Fprintf(w, "  ✗ %s\n", problem)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("%d problem(s) would keep content off astrona.io", len(report.Errors))
	}
	fmt.Fprintf(w, "Validation passed (%d warnings).\n", len(report.Warnings))
	return nil
}

// loadPathYAML loads contentDir/path.yaml and writes its apiVersion/kind
// line to w. Returns the path.yaml location alongside the parsed path.
func loadPathYAML(w io.Writer, contentDir string) (string, *content.TrainingPath, error) {
	pathYAML := filepath.Join(contentDir, "path.yaml")
	if _, err := os.Stat(pathYAML); err != nil {
		return "", nil, fmt.Errorf("path.yaml not found in %s: %w", contentDir, err)
	}

	trainingPath, err := content.LoadTrainingPath(pathYAML)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(w, "%s: apiVersion/kind OK (%s/%s)\n", pathYAML, trainingPath.APIVersion, trainingPath.Kind)
	return pathYAML, trainingPath, nil
}

// ValidateATP checks the course section (Astrona Training Path) in
// contentDir: its path.yaml must load and every stage's content refs must
// resolve through the gitsource cache. Progress goes to w.
func ValidateATP(w io.Writer, contentDir string) error {
	_, trainingPath, err := loadPathYAML(w, contentDir)
	if err != nil {
		return err
	}

	for _, stage := range trainingPath.Spec.Stages {
		for _, item := range stage.Content {
			if item.Repository == "" {
				return fmt.Errorf("stage %s: content ref %s has no repository set", stage.ID, item.Ref)
			}

			fmt.Fprintf(w, "stage %s: resolving %s from %s@%s...\n", stage.ID, item.Ref, item.Repository, item.Version)
			if _, err := gitsource.ResolveGitConfigSource(item.Repository, item.Version, true); err != nil {
				return fmt.Errorf("stage %s: content ref %s: failed to access %s: %w", stage.ID, item.Ref, item.Repository, err)
			}
		}
	}

	fmt.Fprintln(w, "Validation passed.")
	return nil
}

// BuildATP materializes the course section in contentDir into a
// self-contained bundle at output: every stage's content refs are resolved
// (cloned/pulled via the same gitsource cache used by validate) and their
// resolved files are copied under output/<stage.ID>/<ref>, alongside a copy
// of path.yaml at the bundle root. A non-empty output is removed first only
// when clean is set. Progress goes to w.
func BuildATP(w io.Writer, contentDir, output string, clean bool) error {
	pathYAML, trainingPath, err := loadPathYAML(w, contentDir)
	if err != nil {
		return err
	}

	absOutput, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path %s: %w", output, err)
	}

	if entries, statErr := os.ReadDir(absOutput); statErr == nil && len(entries) > 0 {
		if !clean {
			return fmt.Errorf("output directory %s already exists and is not empty (pass --clean to remove it first)", absOutput)
		}
		if err := os.RemoveAll(absOutput); err != nil {
			return fmt.Errorf("failed to clean output directory %s: %w", absOutput, err)
		}
	}

	if err := os.MkdirAll(absOutput, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", absOutput, err)
	}

	for _, stage := range trainingPath.Spec.Stages {
		for _, item := range stage.Content {
			if item.Repository == "" {
				return fmt.Errorf("stage %s: content ref %s has no repository set", stage.ID, item.Ref)
			}

			fmt.Fprintf(w, "stage %s: resolving %s from %s@%s...\n", stage.ID, item.Ref, item.Repository, item.Version)
			repoDir, err := gitsource.ResolveGitConfigSource(item.Repository, item.Version, true)
			if err != nil {
				return fmt.Errorf("stage %s: content ref %s: failed to access %s: %w", stage.ID, item.Ref, item.Repository, err)
			}

			srcPath := repoDir
			if item.Path != "" && item.Path != "." {
				srcPath, err = config.JoinWithinBaseDir(repoDir, item.Path)
				if err != nil {
					return fmt.Errorf("stage %s: content ref %s: %w", stage.ID, item.Ref, err)
				}
			}

			destPath, err := config.JoinWithinBaseDir(absOutput, filepath.Join(stage.ID, item.Ref))
			if err != nil {
				return fmt.Errorf("stage %s: content ref %s: %w", stage.ID, item.Ref, err)
			}

			fmt.Fprintf(w, "stage %s: copying %s into %s...\n", stage.ID, item.Ref, destPath)
			if err := copyDir(srcPath, destPath); err != nil {
				return fmt.Errorf("stage %s: content ref %s: failed to copy %s into %s: %w", stage.ID, item.Ref, srcPath, destPath, err)
			}
		}
	}

	pathYAMLOut := filepath.Join(absOutput, "path.yaml")
	data, err := os.ReadFile(pathYAML)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", pathYAML, err)
	}
	if err := os.WriteFile(pathYAMLOut, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", pathYAMLOut, err)
	}

	fmt.Fprintf(w, "Build complete. Output: %s\n", absOutput)
	return nil
}

// copyDir recursively copies srcDir's contents into destDir, creating
// destDir if needed and skipping .git (a resolved content ref is a clean
// checkout — its git history has no place in the built bundle). Symlinks
// are skipped rather than followed or copied as-is, since a ref pulled from
// a remote content repository is not a fully trusted input.
func copyDir(srcDir, destDir string) error {
	srcRoot, err := os.OpenRoot(srcDir)
	if err != nil {
		return fmt.Errorf("failed to open source root %s: %w", srcDir, err)
	}
	defer srcRoot.Close()

	return filepath.Walk(srcDir, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			return err
		}
		if relPath == "." {
			return os.MkdirAll(destDir, 0755)
		}

		if relPath == ".git" || strings.HasPrefix(relPath, ".git"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		destPath := filepath.Join(destDir, relPath)

		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		srcFile, err := srcRoot.Open(relPath)
		if err != nil {
			return fmt.Errorf("failed to open %s: %w", srcPath, err)
		}
		data, err := io.ReadAll(srcFile)
		srcFile.Close()
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", srcPath, err)
		}
		return os.WriteFile(destPath, data, info.Mode())
	})
}

// DefaultBlueprint is the blueprint repository `astrona content init`
// clones for contentType ("ats" or "atp") when no --repo is given.
func DefaultBlueprint(contentType string) string {
	if contentType == "ats" {
		return atsBlueprint
	}
	return atpBlueprint
}

// ContentInitOptions is one `astrona content init` run. The template
// variables, GitHub settings and output streams come from the command's
// flags; cmd decides the blueprint (Repo) before the call.
type ContentInitOptions struct {
	ContentType string // "ats" or "atp", already validated
	Path        string // target directory as given; resolved to an absolute path
	Repo        string // blueprint repository URL to clone

	// Template variables.
	PathID      string
	Slug        string
	Title       string
	Description string
	Version     string
	Language    string
	AuthorName  string
	License     string

	// Codeowners is written to .github/CODEOWNERS unless empty or "false".
	Codeowners string

	// GitHub repository setup (CreateRepo).
	CreateRepo   bool
	GitHubOrg    string
	GitHubOrgSet bool // --github-org was passed explicitly
	GitHubRepo   string
	GitHubPublic bool

	// Stdout and Stderr receive progress and the output of git and gh.
	Stdout io.Writer
	Stderr io.Writer
}

// InitContent scaffolds a new lab config directory (ATS) or course section
// directory (ATP) at opts.Path from the opts.Repo blueprint, substituting the
// template variables in file names and text files, then optionally writes
// CODEOWNERS and creates, commits and pushes a GitHub repository.
func InitContent(opts ContentInitOptions) error {
	out := opts.Stdout
	path := opts.Path

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path %s: %w", path, err)
	}

	// Pre-flight check: see if the target directory already contains any of the key files to avoid overwriting
	configPath := filepath.Join(absPath, "config.yaml")
	readmePath := filepath.Join(absPath, "README.md")
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("lab configuration already exists at %s", configPath)
	}
	if _, err := os.Stat(readmePath); err == nil {
		return fmt.Errorf("README already exists at %s", readmePath)
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git not found in PATH: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "astrona-template-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	repoURL := opts.Repo

	fmt.Fprintf(out, "Cloning blueprint from %s...\n", repoURL)
	cmdClone := exec.Command(gitPath, "clone", "--depth", "1", repoURL, tempDir)
	cmdClone.Stdout = opts.Stdout
	cmdClone.Stderr = opts.Stderr
	if err := cmdClone.Run(); err != nil {
		return fmt.Errorf("failed to clone template repository %s: %w", repoURL, err)
	}

	// Ensure target directory exists
	if err := os.MkdirAll(absPath, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", absPath, err)
	}

	// Collect vars
	vars := map[string]string{
		"path_id":     opts.PathID,
		"slug":        opts.Slug,
		"title":       opts.Title,
		"description": opts.Description,
		"version":     opts.Version,
		"language":    opts.Language,
		"author_name": opts.AuthorName,
		"license":     opts.License,
	}

	// File copy and template substitution
	srcRoot, err := os.OpenRoot(tempDir)
	if err != nil {
		return fmt.Errorf("failed to open template root %s: %w", tempDir, err)
	}
	defer srcRoot.Close()

	err = filepath.Walk(tempDir, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(tempDir, srcPath)
		if err != nil {
			return err
		}

		// Skip .git
		if relPath == ".git" || strings.HasPrefix(relPath, ".git/") || strings.HasPrefix(relPath, ".git\\") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if relPath == "." {
			return nil
		}

		// Substitute placeholders in target path too!
		destRelPath := relPath
		replacements := []struct {
			placeholder string
			val         string
		}{
			{"{{path_id}}", vars["path_id"]},
			{"{{.path_id}}", vars["path_id"]},
			{"{{.PathID}}", vars["path_id"]},
			{"{{pathID}}", vars["path_id"]},
			{"{{slug}}", vars["slug"]},
			{"{{.slug}}", vars["slug"]},
			{"{{.Slug}}", vars["slug"]},
			{"{{title}}", vars["title"]},
			{"{{.title}}", vars["title"]},
			{"{{.Title}}", vars["title"]},
			{"{{description}}", vars["description"]},
			{"{{.description}}", vars["description"]},
			{"{{.Description}}", vars["description"]},
			{"{{version}}", vars["version"]},
			{"{{.version}}", vars["version"]},
			{"{{.Version}}", vars["version"]},
			{"{{language}}", vars["language"]},
			{"{{.language}}", vars["language"]},
			{"{{.Language}}", vars["language"]},
			{"{{author_name}}", vars["author_name"]},
			{"{{.author_name}}", vars["author_name"]},
			{"{{.AuthorName}}", vars["author_name"]},
			{"{{license}}", vars["license"]},
			{"{{.license}}", vars["license"]},
			{"{{.License}}", vars["license"]},
		}

		for _, r := range replacements {
			destRelPath = strings.ReplaceAll(destRelPath, r.placeholder, r.val)
		}

		destPath := filepath.Join(absPath, destRelPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		srcFile, err := srcRoot.Open(relPath)
		if err != nil {
			return fmt.Errorf("failed to open template file %s: %w", srcPath, err)
		}
		data, err := io.ReadAll(srcFile)
		srcFile.Close()
		if err != nil {
			return fmt.Errorf("failed to read template file %s: %w", srcPath, err)
		}

		ext := strings.ToLower(filepath.Ext(srcPath))
		isText := false
		textExts := []string{".yaml", ".yml", ".md", ".sh", ".json", ".txt", ".ini", ".conf", ".cfg", ".xml", ""}
		for _, tx := range textExts {
			if ext == tx {
				isText = true
				break
			}
		}

		if isText {
			contentStr := string(data)
			for _, r := range replacements {
				contentStr = strings.ReplaceAll(contentStr, r.placeholder, r.val)
			}

			// Go text/template fallback
			tVars := make(map[string]interface{})
			for k, v := range vars {
				tVars[k] = v
				if k == "path_id" {
					tVars["PathID"] = v
				}
				if k == "author_name" {
					tVars["AuthorName"] = v
				}
			}

			tmpl, err := template.New("file").Option("missingkey=zero").Parse(contentStr)
			if err == nil {
				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, tVars); err == nil {
					contentStr = buf.String()
				}
			}

			data = []byte(contentStr)
		}

		return os.WriteFile(destPath, data, info.Mode())
	})

	if err != nil {
		return fmt.Errorf("failed to scaffold template: %w", err)
	}

	// Generate CODEOWNERS if enabled and not "false"
	trimmedCodeowners := strings.TrimSpace(opts.Codeowners)
	if trimmedCodeowners != "" && strings.ToLower(trimmedCodeowners) != "false" {
		codeownersPath := filepath.Join(absPath, ".github", "CODEOWNERS")
		if _, err := os.Stat(codeownersPath); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(codeownersPath), 0755); err != nil {
				return fmt.Errorf("failed to create .github directory: %w", err)
			}
			// If they kept the default but customized the github-org, adapt the default owner
			resolvedOwners := trimmedCodeowners
			if resolvedOwners == "@astrona-io/maintainers" && opts.GitHubOrgSet && opts.GitHubOrg != "" && opts.GitHubOrg != "astrona-io" {
				resolvedOwners = fmt.Sprintf("@%s/maintainers", opts.GitHubOrg)
			}
			content := fmt.Sprintf("# CODEOWNERS for %s\n*       %s\n", opts.PathID, resolvedOwners)
			if err := os.WriteFile(codeownersPath, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to write CODEOWNERS: %w", err)
			}
			fmt.Fprintf(out, "Generated .github/CODEOWNERS file with owners: %s\n", resolvedOwners)
		}
	}

	// Create GitHub Repository automatically if requested
	if opts.CreateRepo {
		if err := createGitHubRepo(opts, gitPath, absPath); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "Successfully initialized Symmetrical %s path at %s using blueprint %s!\n", opts.ContentType, path, repoURL)
	return nil
}

// createGitHubRepo creates the GitHub repository for a scaffolded directory
// with gh (unless it already exists), then initializes, commits and pushes
// absPath to it.
func createGitHubRepo(opts ContentInitOptions, gitPath, absPath string) error {
	out := opts.Stdout

	resolvedRepoName := opts.GitHubRepo
	if resolvedRepoName == "" {
		resolvedRepoName = strings.ToUpper(opts.PathID)
	}

	resolvedOrg := opts.GitHubOrg
	if resolvedOrg == "" {
		resolvedOrg = "astrona-io"
	}

	repoFullName := fmt.Sprintf("%s/%s", resolvedOrg, resolvedRepoName)

	// Find gh
	ghBin, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("gh CLI not found in PATH (required for --create-repo). Please install the GitHub CLI: https://cli.github.com: %w", err)
	}

	// Check if repo already exists
	fmt.Fprintf(out, "Checking if GitHub repository %s already exists...\n", repoFullName)
	cmdCheck := exec.Command(ghBin, "repo", "view", repoFullName)
	repoExists := cmdCheck.Run() == nil

	if repoExists {
		ui.Infof("GitHub repository %s already exists. Skipping remote creation.", repoFullName)
	} else {
		// Create repo
		fmt.Fprintf(out, "Creating GitHub repository %s...\n", repoFullName)
		visFlag := "--public"
		if !opts.GitHubPublic {
			visFlag = "--private"
		}
		cmdCreate := exec.Command(ghBin, "repo", "create", repoFullName, visFlag, "--confirm")
		cmdCreate.Stdout = opts.Stdout
		cmdCreate.Stderr = opts.Stderr
		if err := cmdCreate.Run(); err != nil {
			return fmt.Errorf("failed to create GitHub repository %s: %w", repoFullName, err)
		}
		fmt.Fprintf(out, "Successfully created public GitHub repository: https://github.com/%s\n", repoFullName)
	}

	// Local git initialization and initial commit
	fmt.Fprintln(out, "Initializing local git repository...")
	runGitCmd := func(dir string, args ...string) error {
		c := exec.Command(gitPath, args...)
		c.Dir = dir
		c.Stdout = opts.Stdout
		c.Stderr = opts.Stderr
		return c.Run()
	}

	if _, err := os.Stat(filepath.Join(absPath, ".git")); os.IsNotExist(err) {
		if err := runGitCmd(absPath, "init"); err != nil {
			return fmt.Errorf("failed to run git init: %w", err)
		}
		// Ensure branch is main
		_ = runGitCmd(absPath, "checkout", "-b", "main")
	}

	// Stage and commit files
	fmt.Fprintln(out, "Staging and committing initial files...")
	_ = runGitCmd(absPath, "add", ".")
	cmdCommit := exec.Command(gitPath, "commit", "-m", "feat: initial scaffold")
	cmdCommit.Dir = absPath
	_ = cmdCommit.Run() // ignore if nothing to commit

	// Link remote
	remoteURL := fmt.Sprintf("git@github.com:%s/%s.git", resolvedOrg, resolvedRepoName)
	fmt.Fprintf(out, "Linking remote origin to %s...\n", remoteURL)
	_ = runGitCmd(absPath, "remote", "remove", "origin") // clear if already exists
	if err := runGitCmd(absPath, "remote", "add", "origin", remoteURL); err != nil {
		return fmt.Errorf("failed to add remote origin: %w", err)
	}

	// Attempt push to remote
	fmt.Fprintln(out, "Pushing initial commit to origin main...")
	if err := runGitCmd(absPath, "push", "-u", "origin", "main"); err != nil {
		ui.Warnf("Failed to push to remote: %v. Please push manually.", err)
	} else {
		fmt.Fprintln(out, "Successfully pushed scaffold to GitHub origin main!")
	}
	return nil
}
