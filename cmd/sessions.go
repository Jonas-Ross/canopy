package cmd

import (
	"github.com/spf13/cobra"
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Inspect Claude Code sessions",
	Long: `sessions groups the read-only Claude Code session commands canopy
will expose alongside the TUI (list, tail). In M5 each leaf is a placeholder
that prints a not-implemented message.`,
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Claude Code sessions indexed by canopy",
	Long: `list will enumerate sessions across ~/.claude/projects with their
project, last-event time, and event count. Placeholder in M5.`,
	RunE: stubRunE("sessions list"),
}

var sessionsTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Stream new events from a Claude Code session",
	Long: `tail will follow a session's JSONL log and print normalized events as
they arrive. Placeholder in M5.`,
	RunE: stubRunE("sessions tail"),
}

func init() {
	sessionsCmd.AddCommand(sessionsListCmd, sessionsTailCmd)
	rootCmd.AddCommand(sessionsCmd)
}
