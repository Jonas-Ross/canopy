package tui_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/jonasross/canopy/tui"
)

// Tests for the bundled ops-hygiene fixes:
//   - #23: removeWorktreeCmd / createWorktreeCmd accept and honor the caller's
//     ctx (previously they spun up their own context.Background() and ignored
//     cancellation — so pressing `q` mid-`git worktree remove` left the
//     subprocess running).
//   - #25: error notices strip the "exit status N:" prefix and kill-signal
//     errors include the offending PID.

// TestRemoveWorktreeCmd_CancelledCtxPropagatesToExec proves the caller's ctx
// reaches exec.CommandContext: with a pre-cancelled ctx, exec returns
// context.Canceled before forking git, so the surfaced error is literally
// "context canceled". Without propagation, git would still run and produce
// its own "fatal: … is not a working tree" — distinguishable by string,
// which is the only way to actually guard the regression (an elapsed-time
// check alone passes in either branch because git fails fast on bad paths).
func TestRemoveWorktreeCmd_CancelledCtxPropagatesToExec(t *testing.T) {
	t.Setenv("CANOPY_DEMO", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := tui.RemoveWorktreeCmdForTestCtx(ctx, "/tmp/canopy-test-not-a-worktree")
	msg := cmd()

	// Route through Update so we don't need to peek at unexported msg fields.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(msg)
	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "prune failed") {
		t.Errorf("notice = %q, want 'prune failed …' (cancelled ctx must surface as a prune error)", notice)
	}
	if !strings.Contains(notice, "context canceled") {
		t.Errorf("notice = %q, want 'context canceled' — exec.CommandContext only emits this when the ctx was actually propagated; git's natural failure reads 'fatal: … is not a working tree'", notice)
	}
}

// TestCreateWorktreeCmd_CancelledCtxPropagatesToExec is the analogous
// propagation check on createWorktreeCmd. repoRoot is set to a real git
// repo (initialized in the test) so that broken code wouldn't fast-fail on
// "not a git repository" before reaching exec — i.e. if propagation
// regresses, we'd see git's natural "branch already exists" / cwd error,
// not "context canceled".
func TestCreateWorktreeCmd_CancelledCtxPropagatesToExec(t *testing.T) {
	t.Setenv("CANOPY_DEMO", "")

	repoRoot := t.TempDir()
	if out, err := exec.Command("git", "-C", repoRoot, "init", "--initial-branch=main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// `git worktree add` needs at least one commit to point HEAD at. -c
	// flags must precede the subcommand to set committer identity (the
	// test env may not have user.email/name configured).
	if out, err := exec.Command("git", "-C", repoRoot,
		"-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-m", "seed").CombinedOutput(); err != nil {
		t.Fatalf("git seed commit: %v\n%s", err, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := tui.CreateWorktreeCmdForTestCtx(ctx, repoRoot, "feat/canopy-ctx-test", "main")
	msg := cmd()

	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(msg)
	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "create failed") {
		t.Errorf("notice = %q, want 'create failed …' (cancelled ctx must surface as a create error)", notice)
	}
	if !strings.Contains(notice, "context canceled") {
		t.Errorf("notice = %q, want 'context canceled' — exec.CommandContext only emits this when the ctx was actually propagated; without propagation git would run against the real repo and either succeed or emit its own failure mode", notice)
	}
}

// TestCleanExecErr_StripsExitStatusPrefix is the core of #25: a wrapped
// exec error whose Error() reads "exit status 128: fatal: …" should be
// reduced to just the stderr tail.
func TestCleanExecErr_StripsExitStatusPrefix(t *testing.T) {
	err := errors.New("exit status 128")
	got := tui.CleanExecErrForTest(err, []byte("  fatal: not a working tree\n"))
	if got == nil {
		t.Fatal("CleanExecErrForTest returned nil; want non-nil error")
	}
	if got.Error() != "fatal: not a working tree" {
		t.Errorf("formatted = %q, want 'fatal: not a working tree' (whitespace trimmed, exit-status prefix dropped)", got.Error())
	}
	if strings.Contains(got.Error(), "exit status") {
		t.Errorf("formatted = %q still contains 'exit status' prefix", got.Error())
	}
}

// TestCleanExecErr_EmptyOutputReturnsOriginal: when stderr is empty there's
// nothing to substitute, so the original err is preserved.
func TestCleanExecErr_EmptyOutputReturnsOriginal(t *testing.T) {
	err := errors.New("exit status 1")
	for _, out := range [][]byte{nil, {}, []byte("   \n  ")} {
		got := tui.CleanExecErrForTest(err, out)
		if got == nil || got.Error() != err.Error() {
			t.Errorf("CleanExecErrForTest(err, %q) = %v, want original err %q", string(out), got, err.Error())
		}
	}
}

// TestWrapKillSignalError_IncludesPID covers the second half of #25: the
// kill error notice was "kill failed: operation not permitted" with no PID
// — useless for diagnosis when several processes failed.
func TestWrapKillSignalError_IncludesPID(t *testing.T) {
	inner := errors.New("operation not permitted")
	got := tui.WrapKillSignalErrorForTest(12345, inner)
	if got == nil {
		t.Fatal("WrapKillSignalErrorForTest returned nil")
	}
	if !strings.Contains(got.Error(), "12345") {
		t.Errorf("wrapped = %q, want PID 12345 in message", got.Error())
	}
	if !strings.Contains(got.Error(), "operation not permitted") {
		t.Errorf("wrapped = %q, want inner err message preserved", got.Error())
	}
	if !errors.Is(got, inner) {
		t.Errorf("errors.Is(wrapped, inner) = false; wrap must use %%w so callers can unwrap")
	}
}

// TestRemoveWorktreeCmd_NoticeStripsExitStatus is the end-to-end #25
// assertion: running removeWorktreeCmd against a path git refuses must
// produce a notice that contains the actual git error message but NOT
// the "exit status N:" noise.
func TestRemoveWorktreeCmd_NoticeStripsExitStatus(t *testing.T) {
	t.Setenv("CANOPY_DEMO", "")

	// /tmp/<unique>: a path that's definitely not a registered worktree.
	cmd := tui.RemoveWorktreeCmdForTestCtx(context.Background(), "/tmp/canopy-not-a-worktree-xyz")
	msg := cmd()

	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(msg)
	notice := stripANSI(tui.NoticeOf(m))

	if !strings.Contains(notice, "prune failed") {
		t.Fatalf("notice = %q, want 'prune failed: …'", notice)
	}
	if strings.Contains(notice, "exit status") {
		t.Errorf("notice = %q contains 'exit status' prefix; #25 expects it stripped", notice)
	}
}
