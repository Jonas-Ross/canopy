package cmd

import (
	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Inspect and manage git worktrees",
	Long: `worktree groups the worktree-management commands canopy will eventually
expose from its operational TUI (list, new, prune). In M5 each leaf is a
placeholder that prints a not-implemented message.`,
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List git worktrees and their state",
	Long: `list will print the worktrees canopy knows about along with branch,
ahead/behind, and dirty status. Placeholder in M5.`,
	RunE: stubRunE("worktree list"),
}

var worktreeNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new worktree",
	Long: `new will scaffold a new worktree for a branch off the main repo.
Placeholder in M5.`,
	RunE: stubRunE("worktree new"),
}

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove worktrees that no longer have an upstream",
	Long: `prune will remove worktrees whose branch has been merged or deleted
upstream. Placeholder in M5; the standalone "canopy prune" command will
eventually call into the same path.`,
	RunE: stubRunE("worktree prune"),
}

func init() {
	worktreeCmd.AddCommand(worktreeListCmd, worktreeNewCmd, worktreePruneCmd)
	rootCmd.AddCommand(worktreeCmd)
}
