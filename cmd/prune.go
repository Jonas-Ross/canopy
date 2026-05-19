package cmd

import (
	"github.com/spf13/cobra"
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove worktrees whose upstream branch is gone",
	Long: `prune is a top-level shortcut for the worktree-prune action. The
eventual implementation will call into the same code path as
"canopy worktree prune". Placeholder in M5.`,
	RunE: stubRunE("prune"),
}

func init() {
	rootCmd.AddCommand(pruneCmd)
}
