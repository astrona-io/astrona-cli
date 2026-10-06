package main

import "github.com/spf13/cobra"

func init() {
	// Help lists commands in the order they're added: by workflow, which
	// reads better than alphabetical (run, docs, shell, submit, …).
	cobra.EnableCommandSorting = false
}

type commandGroup struct {
	id, title string
	cmds      []*cobra.Command
}

// commandGroups is the root help's layout: what a student needs first,
// then managing running labs, authoring, and troubleshooting.
func commandGroups(flags *rootFlags) []commandGroup {
	return []commandGroup{
		{"lab", "Take a lab:", []*cobra.Command{
			newUseCmd(flags),
			newRunCmd(flags),
			newDocsCmd(flags),
			newShellCmd(flags),
			newSSHCmd(),
			newSubmitCmd(flags),
			newStatusCmd(flags),
			newProgressCmd(),
			newResetCmd(flags),
			newDestroyCmd(flags),
		}},
		{"manage", "Manage running labs:", []*cobra.Command{
			newListCmd(),
			newStopCmd(flags),
			newStartCmd(flags),
			newPortForwardCmd(flags),
			newNetCmd(flags),
			newKubeconfigCmd(flags),
		}},
		{"author", "Write labs (authors and teachers):", []*cobra.Command{
			newInitCmd(),
			newContentCmd(),
			newValidateCmd(flags),
			newTestCmd(flags),
			newBundleCmd(flags),
		}},
		{"debug", "Troubleshoot:", []*cobra.Command{
			newCheckCmd(flags),
			newDiagnoseCmd(flags),
			newLogsCmd(),
			newImagesCmd(),
		}},
	}
}
