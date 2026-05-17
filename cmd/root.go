package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "canopy",
	Short: "An overhead view of the whole forest of your work",
	Long: `Canopy is a worktree-aware git command center fused with Claude Code
session forensics — see all your parallel work, agent sessions, PRs, and
processes at once.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.ErrOrStderr(), "canopy: TUI not yet implemented — see docs/handoff.md")
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
