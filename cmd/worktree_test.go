package cmd

import (
	"strings"
	"testing"
)

func TestWorktreeList_PrintsStub(t *testing.T) {
	got := runRootCmd(t, "worktree", "list")
	if !strings.Contains(got, "canopy worktree list: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestWorktreeNew_PrintsStub(t *testing.T) {
	got := runRootCmd(t, "worktree", "new")
	if !strings.Contains(got, "canopy worktree new: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestWorktreePrune_PrintsStub(t *testing.T) {
	got := runRootCmd(t, "worktree", "prune")
	if !strings.Contains(got, "canopy worktree prune: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

// TestWorktree_HelpListsChildren verifies that invoking the parent without
// a leaf falls through to cobra's auto-generated help, which must mention
// each child subcommand by name.
func TestWorktree_HelpListsChildren(t *testing.T) {
	got := runRootCmd(t, "worktree", "--help")
	for _, child := range []string{"list", "new", "prune"} {
		if !strings.Contains(got, child) {
			t.Errorf("worktree --help missing child %q; got %q", child, got)
		}
	}
}
