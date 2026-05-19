package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// stubRunE returns a cobra RunE that prints a uniform M5 placeholder line
// to stderr and exits cleanly. name should be the space-joined command path
// (e.g. "worktree list", "prune"). The helper exists so the placeholder text
// stays uniform across the six leaves added in M5 and can be grepped &
// removed wholesale as real implementations land.
func stubRunE(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		fmt.Fprintf(cmd.ErrOrStderr(), "canopy %s: not yet implemented (M5 placeholder)\n", name)
		return nil
	}
}
