package tui_test

import (
	"strings"
	"testing"

	"github.com/jonasross/canopy/tui"
)

// Tests for branch-name validation surfaced by the silent-failure audit:
// a branch beginning with '-' would be parsed as a flag by git when fed
// as a positional arg; '..' would let the worktree path traverse out of
// the .worktrees/ base dir.

func TestValidBranchOrBaseName_AcceptsCommonForms(t *testing.T) {
	good := []string{
		"main",
		"feat/auth",
		"feat/auth.v2",
		"feature_42",
		"release-1.0",
		"hotfix/v1-2-3",
	}
	for _, s := range good {
		if !tui.ValidBranchOrBaseName(s) {
			t.Errorf("ValidBranchOrBaseName(%q) = false, want true", s)
		}
	}
}

func TestValidBranchOrBaseName_RejectsDangerousInputs(t *testing.T) {
	bad := []string{
		"",                        // empty
		"-foo",                    // leading dash → flag injection
		"--upload-pack=touch /x",  // dash-dash flag form
		"foo..bar",                // path traversal segment
		"..",                      // path traversal
		"feat:branch",             // colon (git ref syntax)
		"feat branch",             // space
		"feat$branch",             // shell metachar
		"feat;rm",                 // command injection if ever shelled
		"feat\nbranch",            // newline
	}
	for _, s := range bad {
		if tui.ValidBranchOrBaseName(s) {
			t.Errorf("ValidBranchOrBaseName(%q) = true, want false", s)
		}
	}
}

// TestCreateWorktree_DemoModeSucceedsWithoutShelling drives createWorktreeCmd
// under CANOPY_DEMO=1 and confirms it returns success without touching the
// filesystem. The same code path covers the soft-gate that was missing
// before this fix.
func TestCreateWorktree_DemoModeSucceedsWithoutShelling(t *testing.T) {
	t.Setenv("CANOPY_DEMO", "1")
	cmd := tui.CreateWorktreeCmdForTest("/repo/root", "feat/x", "main")
	msg := cmd()
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(msg)
	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "created feat/x") {
		t.Errorf("soft-gated create notice = %q, want 'created feat/x …'", notice)
	}
}

// TestCreateWorktree_InvalidBranchEmitsErrorBeforeShelling confirms the
// validator short-circuits before any os/exec call. We run with
// CANOPY_DEMO=0 to bypass the soft-gate but the validator rejects first.
func TestCreateWorktree_InvalidBranchEmitsErrorBeforeShelling(t *testing.T) {
	t.Setenv("CANOPY_DEMO", "")
	cmd := tui.CreateWorktreeCmdForTest("/repo/root", "--upload-pack=touch /tmp/owned", "main")
	msg := cmd()
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(msg)
	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "create failed") || !strings.Contains(notice, "invalid branch") {
		t.Errorf("validator notice = %q, want 'create failed: invalid branch …'", notice)
	}
}
