package cmd

import (
	"bytes"
	"testing"
)

// runRootCmd dispatches args through rootCmd and returns the combined
// stdout+stderr output. Tests merge the two sinks because stubRunE writes
// to stderr while cobra's help routine writes to stdout — asserting on one
// alone would miss the other surface.
func runRootCmd(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String()
}
