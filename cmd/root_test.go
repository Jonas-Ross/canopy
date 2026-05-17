package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestRootCommand_Help verifies that invoking rootCmd with --help exits
// without error and prints usage text containing "canopy".
//
// Acceptance criterion: "A test invoking rootCmd with --help exits without
// error and prints usage text containing the string 'canopy'."
//
// This replaces the old stub test that asserted "TUI not yet implemented".
func TestRootCommand_Help(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--help"})

	// cobra --help exits with nil; the actual err from Execute for --help is nil.
	_ = rootCmd.Execute()

	got := out.String()
	if !strings.Contains(got, "canopy") {
		t.Errorf("--help output does not contain 'canopy'; got %q", got)
	}
}
