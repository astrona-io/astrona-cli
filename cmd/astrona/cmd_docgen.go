package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// newDocgenCmd generates a markdown page per command into --output, using
// Cobra's own doc generator against the exact command tree newRootCmd builds
// — so docs/reference/cli/ can never drift from the real Use/Short/Long text
// and flags. Hidden: it's a docs-build-time tool for `just build-docs`, not
// something a lab operator ever needs to run.
func newDocgenCmd(flags *rootFlags) *cobra.Command {
	var outputDir string

	cmd := &cobra.Command{
		Use:    "docgen",
		Short:  "Generate CLI reference markdown (docs build tooling, not for end users)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A fresh tree built by newRootCmd never has docgen itself
			// attached, so the generated reference only covers the CLI's
			// real public surface.
			root := newRootCmd(flags)
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create CLI reference output dir %s: %w", outputDir, err)
			}
			if err := doc.GenMarkdownTree(root, outputDir); err != nil {
				return fmt.Errorf("generate CLI reference markdown: %w", err)
			}

			// Post-process generated files to format examples cleanly with separate, copyable code blocks
			if err := postProcessDocs(outputDir); err != nil {
				return fmt.Errorf("post-process generated docs: %w", err)
			}

			fmt.Printf("CLI reference written to %s\n", outputDir)
			return nil
		},
	}

	cmd.Flags().StringVar(&outputDir, "output", "docs/reference/cli", "Output directory for generated CLI reference markdown")

	return cmd
}

// postProcessDocs refines the Examples section of the content init command documentation page
// so that descriptions are rendered as plain-text outside individual copyable code blocks.
func postProcessDocs(outputDir string) error {
	filePath := filepath.Join(outputDir, "astrona_content_init.md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Skip if file wasn't generated
		}
		return err
	}

	content := string(data)

	newBlock := `### Examples

**Example 1: Creating a Standard Theoretical Training Path (ATP)**  
*Resolves: Setting up a structured, textbook-based theoretical course section.*

` + "```sh" + `
astrona content init atp \
  --slug my-new-path \
  --path-id ATP010 \
  --title "My First Training Path" \
  --author-name "Alice Smith"
` + "```" + `


**Example 2: Scaffolding a Hands-On Practical Sandbox Lab (ATS)**  
*Resolves: Setting up a VM/QEMU-backed virtual sandbox environment with custom disks and verification.*

` + "```sh" + `
astrona content init ats ./labs/disk-discovery \
  --slug virtio-disk-discovery \
  --path-id ATS012 \
  --title "Virtio Disk Discovery Lab" \
  --author-name "Bob Jones"
` + "```" + `


**Example 3: Customizing Templates via an External Blueprint Repository**  
*Resolves: Bootstrapping structured content using an organization-specific custom template layout.*

` + "```sh" + `
astrona content init atp \
  --slug custom-path \
  --repo git@github.com:my-org/custom-path-blueprint.git \
  --path-id ATP040 \
  --title "Custom Layout Path" \
  --author-name "Alice Smith"
` + "```" + `


**Example 4: Creating an ATP with Automated GitHub Repository and CODEOWNERS Setup**  
*Resolves: Scaffolding a structured Training Path (ATP) and automatically setting up its remote GitHub repository.*

` + "```sh" + `
astrona content init atp \
  --slug kubernetes-networking-path \
  --path-id ATP102 \
  --title "Kubernetes Advanced Networking Path" \
  --author-name "Alice Smith" \
  --create-repo \
  --github-org "my-org" \
  --github-repo "K8S-NETWORKING-ATP" \
  --codeowners
` + "```"

	startIdx := strings.Index(content, "### Examples")
	optionsIdx := strings.Index(content, "### Options")
	if startIdx != -1 && optionsIdx != -1 {
		// Cut out the Examples section completely from its original position (at the top)
		content = content[:startIdx] + content[optionsIdx:]
	}

	seeAlsoIdx := strings.Index(content, "### SEE ALSO")
	if seeAlsoIdx != -1 {
		// Insert our beautiful newBlock (Examples) right before "### SEE ALSO" (at the bottom)
		content = content[:seeAlsoIdx] + newBlock + "\n\n" + content[seeAlsoIdx:]
	}

	return os.WriteFile(filePath, []byte(content), 0644)
}
