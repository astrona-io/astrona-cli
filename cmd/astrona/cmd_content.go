package main

import (
	"fmt"
	"os"
	"strings"

	"astrona/internal/authoring"

	"github.com/spf13/cobra"
)

// newContentCmd builds `astrona content` command tree.
func newContentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "content",
		Short: "Validate, build and scaffold course content (sections of labs)",
		Long:  "Manage and scaffold lab content inside the Astrona ecosystem. This command suite is specifically tailored for teachers, instructors, and content authors to help create, structure, and bootstrap hands-on labs (ATS) and course sections (ATP).",
	}

	cmd.AddCommand(newContentInitCmd())
	cmd.AddCommand(newContentValidateCmd())
	cmd.AddCommand(newContentBuildCmd())

	return cmd
}

// newContentValidateCmd builds `astrona content validate <type> <source>` which
// validates a lab config directory (ATS) or course section directory (ATP).
// source is either a local folder path (validated as-is) or a git repository
// URL (cloned/updated via the same gitsource cache used by --git elsewhere in
// this CLI), optionally pinned to a branch or tag with --git-ref.
func newContentValidateCmd() *cobra.Command {
	var gitRef string

	cmd := &cobra.Command{
		Use:   "validate <type> <source>",
		Short: "Validate a lab or section directory (for teachers and authors)",
		Long: "Validate a training repository (Astrona Training Series - ATS, 'ats': its astrona.yaml — readings, " +
			"labs, playgrounds and quiz question banks, checked the way astrona.io reads them) or a course section directory " +
			"(Astrona Training Path - ATP, 'atp'). source is either a local folder path (the raw content files) or a " +
			"git repository URL (https://, git@host:, ssh://, or a .git suffix), optionally pinned to a branch or " +
			"tag with --git-ref.",
		Example: `  astrona content validate ats ./ATS014
  astrona content validate ats https://github.com/astrona-io/ATS014.git --git-ref main
  astrona content validate atp ./paths/kubernetes-networking
  astrona content validate atp https://github.com/astrona-io/kubernetes-networking-atp.git --git-ref v1.2.0
  astrona content validate atp git@github.com:astrona-io/kubernetes-networking-atp.git --git-ref main`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			contentType := strings.ToLower(strings.TrimSpace(args[0]))
			if contentType != "ats" && contentType != "atp" {
				return fmt.Errorf("invalid content type: %q. Must be 'ats' (Astrona Training Series) or 'atp' (Astrona Training Path)", args[0])
			}
			source := args[1]

			contentDir, err := authoring.ResolveContentSource(source, gitRef)
			if err != nil {
				return err
			}
			if contentType == "ats" {
				return authoring.ValidateATS(cmd.OutOrStdout(), contentDir)
			}
			return authoring.ValidateATP(os.Stdout, contentDir)
		},
	}

	cmd.Flags().StringVar(&gitRef, "git-ref", "", "Git branch, tag, or commit to check out (only used when source is a git URL)")

	return cmd
}

// newContentBuildCmd builds `astrona content build <type> <source>` which
// materializes a course section directory (ATP) into a self-contained
// output bundle: every stage's content refs are resolved (cloned/pulled via
// the same gitsource cache used by validate) and their resolved files are
// copied under output/<stage.ID>/<ref>, alongside a copy of path.yaml at
// the bundle root.
func newContentBuildCmd() *cobra.Command {
	var (
		gitRef string
		output string
		clean  bool
	)

	cmd := &cobra.Command{
		Use:   "build <type> <source> [flags]",
		Short: "Materialize a course section into a self-contained bundle (for teachers and authors)",
		Long: "Build a course section directory (Astrona Training Path - ATP, 'atp') into a self-contained output " +
			"bundle: resolves every stage's content refs (typically ATS labs) via git and copies the resolved files " +
			"under --output/<stage-id>/<ref>, alongside a copy of path.yaml at the bundle root. source is either a " +
			"local folder path (the raw content files) or a git repository URL (https://, git@host:, ssh://, or a " +
			".git suffix), optionally pinned to a branch or tag with --git-ref.",
		Example: `  astrona content build atp ./paths/kubernetes-networking --output ./dist
  astrona content build atp https://github.com/astrona-io/kubernetes-networking-atp.git --git-ref v1.2.0 --output ./dist --clean`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			contentType := strings.ToLower(strings.TrimSpace(args[0]))
			if contentType != "ats" && contentType != "atp" {
				return fmt.Errorf("invalid content type: %q. Must be 'ats' (Astrona Training Series) or 'atp' (Astrona Training Path)", args[0])
			}
			if contentType != "atp" {
				return fmt.Errorf("build for content type %q is not yet implemented", contentType)
			}

			source := args[1]

			contentDir, err := authoring.ResolveContentSource(source, gitRef)
			if err != nil {
				return err
			}
			return authoring.BuildATP(os.Stdout, contentDir, output, clean)
		},
	}

	cmd.Flags().StringVar(&gitRef, "git-ref", "", "Git branch, tag, or commit to check out (only used when source is a git URL)")
	cmd.Flags().StringVarP(&output, "output", "o", "./dist", "Output directory for the materialized bundle")
	cmd.Flags().BoolVar(&clean, "clean", false, "Remove the output directory first if it already exists and is not empty")

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

			// Resolve blueprint repository URL based on content type
			repoURL := repo
			if !cmd.Flags().Changed("repo") {
				repoURL = authoring.DefaultBlueprint(contentType)
			}

			return authoring.InitContent(authoring.ContentInitOptions{
				ContentType:  contentType,
				Path:         path,
				Repo:         repoURL,
				PathID:       pathID,
				Slug:         slug,
				Title:        title,
				Description:  description,
				Version:      version,
				Language:     language,
				AuthorName:   authorName,
				License:      license,
				Codeowners:   codeowners,
				CreateRepo:   createRepo,
				GitHubOrg:    githubOrg,
				GitHubOrgSet: cmd.Flags().Changed("github-org"),
				GitHubRepo:   githubRepo,
				GitHubPublic: githubPublic,
				Stdout:       os.Stdout,
				Stderr:       os.Stderr,
			})
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
