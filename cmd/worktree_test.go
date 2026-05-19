package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestWorktreeList_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy worktree list: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestWorktreeNew_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "new"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy worktree new: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestWorktreePrune_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "prune"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy worktree prune: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

// TestWorktree_HelpListsChildren verifies that invoking the parent without
// a leaf falls through to cobra's auto-generated help, which must mention
// each child subcommand by name.
func TestWorktree_HelpListsChildren(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, child := range []string{"list", "new", "prune"} {
		if !strings.Contains(got, child) {
			t.Errorf("worktree --help missing child %q; got %q", child, got)
		}
	}
}
