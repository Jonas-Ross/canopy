package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/sessions"
	"github.com/jonasross/canopy/tui"
)

var rootCmd = &cobra.Command{
	Use:   "canopy",
	Short: "An overhead view of the whole forest of your work",
	Long: `Canopy is a worktree-aware git command center fused with Claude Code
session forensics — see all your parallel work, agent sessions, PRs, and
processes at once.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		// Locate the Claude Code projects root.
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("canopy: resolve home dir: %w", err)
		}
		projectsRoot := filepath.Join(home, ".claude", "projects")

		// Opening the session store takes ~467ms on large histories.
		fmt.Fprintln(cmd.ErrOrStderr(), "Loading sessions…")
		store, err := sessions.Open(projectsRoot)
		if err != nil {
			return fmt.Errorf("canopy: open sessions: %w", err)
		}
		defer store.Close()

		// Discover the repo root for the current working directory.
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("canopy: get cwd: %w", err)
		}
		wts, err := git.ListWorktrees(ctx, cwd)
		if err != nil {
			return fmt.Errorf("canopy: list worktrees: %w", err)
		}
		// The main worktree path is the repo root — use the first worktree's
		// path resolved to its top-level (git worktree list returns absolute paths).
		repoRoot := cwd
		if len(wts) > 0 {
			repoRoot = wts[0].Path
		}

		agg, err := aggregator.New(aggregator.Config{
			Repos:        []aggregator.Repo{{Root: repoRoot}},
			SessionStore: store,
		})
		if err != nil {
			return fmt.Errorf("canopy: create aggregator: %w", err)
		}

		if err := agg.Start(ctx); err != nil {
			return fmt.Errorf("canopy: start aggregator: %w", err)
		}

		return tui.Run(ctx, agg)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
