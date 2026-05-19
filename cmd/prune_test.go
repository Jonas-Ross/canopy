package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrune_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"prune"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy prune: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}
