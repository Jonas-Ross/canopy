package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand_PrintsStub(t *testing.T) {
	var stderr bytes.Buffer
	rootCmd.SetOut(&stderr)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute() returned error: %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "TUI not yet implemented") {
		t.Errorf("expected stderr to contain stub message; got %q", got)
	}
}
