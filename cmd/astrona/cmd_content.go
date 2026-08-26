package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

// newContentCmd builds `astrona content` command tree.
func newContentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "content",
		Short: "Manage and scaffold lab content (for teachers and authors)",
		Long:  "Manage and scaffold lab content inside the Astrona ecosystem. This command suite is specifically tailored for teachers, instructors, and content authors to help create, structure, and bootstrap hands-on labs (ATS) and course sections (ATP).",
	}

	cmd.AddCommand(newContentInitCmd())

	return cmd
}

// newContentInitCmd builds `astrona content init <type> [path]` which scaffolds a new
// lab config directory (ATS) or course section directory (ATP) from a blueprint git repository.
func newContentInitCmd() *cobra.Command {
	var (
		pathID       string
		slug         string
		title        string
		description  string
		version      string
		language     string
		authorName   string
		license      string
		repo         string
		createRepo   bool
		githubOrg    string
		githubRepo   string
		githubPublic bool
		codeowners   string
	)

	cmd := &cobra.Command{
		Use:   "init <type> [path]",
		Short: "Scaffold a new lab or section directory (for teachers and authors)",
		Long:  "Scaffold a new lab config directory (Astrona Training Series - ATS, 'ats') or course section directory (Astrona Training Path - ATP, 'atp') with necessary boilerplate files and directory structure. This command clones a template blueprint repository and injects customizable template arguments.",
		Example: `  Example 1: Creating a Standard Theoretical Training Path (ATP)
  Resolves: Setting up a structured, textbook-based theoretical course section.
  
  astrona content init atp \
    --slug my-new-path \
    --path-id ATP010 \
    --title "My First Training Path" \
    --author-name "Alice Smith"


  Example 2: Scaffolding a Hands-On Practical Sandbox Lab (ATS)
  Resolves: Setting up a VM/QEMU-backed virtual sandbox environment with custom disks and verification.
  
  astrona content init ats ./labs/disk-discovery \
    --slug virtio-disk-discovery \
    --path-id ATS012 \
    --title "Virtio Disk Discovery Lab" \
    --author-name "Bob Jones"


  Example 3: Customizing Templates via an External Blueprint Repository
  Resolves: Bootstrapping structured content using an organization-specific custom template layout.
  
  astrona content init atp \
    --slug custom-path \
    --repo git@github.com:my-org/custom-path-blueprint.git \
    --path-id ATP040 \
    --title "Custom Layout Path" \
    --author-name "Alice Smith"


  Example 4: Creating an ATP with Automated GitHub Repository and CODEOWNERS Setup
  Resolves: Scaffolding a structured Training Path (ATP) and automatically setting up its remote GitHub repository with custom codeowners.
  
  astrona content init atp \
    --slug kubernetes-networking-path \
    --path-id ATP102 \
    --title "Kubernetes Advanced Networking Path" \
    --author-name "Alice Smith" \
    --create-repo \
    --github-org "my-org" \
    --github-repo "K8S-NETWORKING-ATP" \
    --codeowners "@alice @bob @my-org/networking-team"`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			contentType := strings.ToLower(strings.TrimSpace(args[0]))
			if contentType != "ats" && contentType != "atp" {
				return fmt.Errorf("invalid content type: %q. Must be 'ats' (Astrona Training Series) or 'atp' (Astrona Training Path)", args[0])
			}

			// Determine target path
			path := slug
			if len(args) > 1 {
				path = args[1]
			}

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

			// Resolve blueprint repository URL based on content type
			repoURL := repo
			if !cmd.Flags().Changed("repo") {
				if contentType == "ats" {
					repoURL = "git@github.com:astrona-io/training-sandbox-blueprint.git"
				} else {
					repoURL = "git@github.com:astrona-io/training-path-blueprint.git"
				}
			}

			fmt.Printf("Cloning blueprint from %s...\n", repoURL)
			cmdClone := exec.Command(gitPath, "clone", "--depth", "1", repoURL, tempDir)
			cmdClone.Stdout = os.Stdout
			cmdClone.Stderr = os.Stderr
			if err := cmdClone.Run(); err != nil {
				return fmt.Errorf("failed to clone template repository %s: %w", repoURL, err)
			}

			// Ensure target directory exists
			if err := os.MkdirAll(absPath, 0755); err != nil {
				return fmt.Errorf("failed to create target directory %s: %w", absPath, err)
			}

			// Collect vars
			vars := map[string]string{
				"path_id":     pathID,
				"slug":        slug,
				"title":       title,
				"description": description,
				"version":     version,
				"language":    language,
				"author_name": authorName,
				"license":     license,
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
			trimmedCodeowners := strings.TrimSpace(codeowners)
			if trimmedCodeowners != "" && strings.ToLower(trimmedCodeowners) != "false" {
				codeownersPath := filepath.Join(absPath, ".github", "CODEOWNERS")
				if _, err := os.Stat(codeownersPath); os.IsNotExist(err) {
					if err := os.MkdirAll(filepath.Dir(codeownersPath), 0755); err != nil {
						return fmt.Errorf("failed to create .github directory: %w", err)
					}
					// If they kept the default but customized the github-org, adapt the default owner
					resolvedOwners := trimmedCodeowners
					if resolvedOwners == "@astrona-io/maintainers" && cmd.Flags().Changed("github-org") && githubOrg != "" && githubOrg != "astrona-io" {
						resolvedOwners = fmt.Sprintf("@%s/maintainers", githubOrg)
					}
					content := fmt.Sprintf("# CODEOWNERS for %s\n*       %s\n", pathID, resolvedOwners)
					if err := os.WriteFile(codeownersPath, []byte(content), 0644); err != nil {
						return fmt.Errorf("failed to write CODEOWNERS: %w", err)
					}
					fmt.Printf("Generated .github/CODEOWNERS file with owners: %s\n", resolvedOwners)
				}
			}

			// Create GitHub Repository automatically if requested
			if createRepo {
				resolvedRepoName := githubRepo
				if resolvedRepoName == "" {
					resolvedRepoName = strings.ToUpper(pathID)
				}

				resolvedOrg := githubOrg
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
				fmt.Printf("Checking if GitHub repository %s already exists...\n", repoFullName)
				cmdCheck := exec.Command(ghBin, "repo", "view", repoFullName)
				repoExists := cmdCheck.Run() == nil

				if repoExists {
					fmt.Printf("[INFO] GitHub repository %s already exists. Skipping remote creation.\n", repoFullName)
				} else {
					// Create repo
					fmt.Printf("Creating GitHub repository %s...\n", repoFullName)
					visFlag := "--public"
					if !githubPublic {
						visFlag = "--private"
					}
					cmdCreate := exec.Command(ghBin, "repo", "create", repoFullName, visFlag, "--confirm")
					cmdCreate.Stdout = os.Stdout
					cmdCreate.Stderr = os.Stderr
					if err := cmdCreate.Run(); err != nil {
						return fmt.Errorf("failed to create GitHub repository %s: %w", repoFullName, err)
					}
					fmt.Printf("Successfully created public GitHub repository: https://github.com/%s\n", repoFullName)
				}

				// Local git initialization and initial commit
				fmt.Println("Initializing local git repository...")
				runGitCmd := func(dir string, args ...string) error {
					c := exec.Command(gitPath, args...)
					c.Dir = dir
					c.Stdout = os.Stdout
					c.Stderr = os.Stderr
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
				fmt.Println("Staging and committing initial files...")
				_ = runGitCmd(absPath, "add", ".")
				cmdCommit := exec.Command(gitPath, "commit", "-m", "feat: initial scaffold")
				cmdCommit.Dir = absPath
				_ = cmdCommit.Run() // ignore if nothing to commit

				// Link remote
				remoteURL := fmt.Sprintf("git@github.com:%s/%s.git", resolvedOrg, resolvedRepoName)
				fmt.Printf("Linking remote origin to %s...\n", remoteURL)
				_ = runGitCmd(absPath, "remote", "remove", "origin") // clear if already exists
				if err := runGitCmd(absPath, "remote", "add", "origin", remoteURL); err != nil {
					return fmt.Errorf("failed to add remote origin: %w", err)
				}

				// Attempt push to remote
				fmt.Println("Pushing initial commit to origin main...")
				if err := runGitCmd(absPath, "push", "-u", "origin", "main"); err != nil {
					fmt.Printf("[WARN] Failed to push to remote: %v. Please push manually.\n", err)
				} else {
					fmt.Println("Successfully pushed scaffold to GitHub origin main!")
				}
			}

			fmt.Printf("Successfully initialized Symmetrical %s path at %s using blueprint %s!\n", contentType, path, repoURL)
			return nil
		},
	}

	cmd.Flags().StringVarP(&pathID, "path-id", "i", "ATPxxx", "Default value for 'path_id' template variable")
	cmd.Flags().StringVarP(&slug, "slug", "s", "path-slug", "Default value for 'slug' template variable")
	cmd.Flags().StringVarP(&title, "title", "t", "Path Title", "Default value for 'title' template variable")
	cmd.Flags().StringVarP(&description, "description", "d", "A short, engaging description of what the learner will achieve.", "Default value for 'description' template variable")
	cmd.Flags().StringVarP(&version, "version", "v", "0.1.0", "Default value for 'version' template variable")
	cmd.Flags().StringVarP(&language, "language", "g", "en", "Default value for 'language' template variable")
	cmd.Flags().StringVarP(&authorName, "author-name", "a", "Author Name", "Default value for 'author_name' template variable")
	cmd.Flags().StringVarP(&license, "license", "l", "Apache-2.0", "Default value for 'license' template variable")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "Git template repository URL (overrides default blueprints)")

	// GitHub automated setup flags
	cmd.Flags().BoolVar(&createRepo, "create-repo", false, "Automatically create a GitHub repository for the scaffolded content using gh CLI")
	cmd.Flags().StringVar(&githubOrg, "github-org", "astrona-io", "GitHub organization or username for the repository")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "GitHub repository name (defaults to uppercase path-id)")
	cmd.Flags().BoolVar(&githubPublic, "github-public", true, "Make the created GitHub repository public")
	cmd.Flags().StringVar(&codeowners, "codeowners", "@astrona-io/maintainers", "Generate a .github/CODEOWNERS file with specified owners (set to 'false' or empty to disable)")

	return cmd
}
