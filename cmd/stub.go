package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// stubRunE centralises the M5 placeholder message so wording stays uniform
// across the six leaves and the helper can be grepped away wholesale as
// real implementations land. name is the space-joined command path, e.g.
// "worktree list" or "prune".
func stubRunE(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		fmt.Fprintf(cmd.ErrOrStderr(), "canopy %s: not yet implemented (M5 placeholder)\n", name)
		return nil
	}
}
