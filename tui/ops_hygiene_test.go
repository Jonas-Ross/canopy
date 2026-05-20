package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonasross/canopy/tui"
)

// Tests for the bundled ops-hygiene fixes:
//   - #23: removeWorktreeCmd / createWorktreeCmd accept and honor the caller's
//     ctx (previously they spun up their own context.Background() and ignored
//     cancellation — so pressing `q` mid-`git worktree remove` left the
//     subprocess running).
//   - #25: error notices strip the "exit status N:" prefix and kill-signal
//     errors include the offending PID.

// TestRemoveWorktreeCmd_CancelledCtxShortCircuits proves the caller's ctx is
// threaded into exec.CommandContext: a pre-cancelled ctx must trip the
// subprocess immediately (well under git's normal latency) rather than the
// old behaviour of running to completion under an internal 10s timeout.
func TestRemoveWorktreeCmd_CancelledCtxShortCircuits(t *testing.T) {
	t.Setenv("CANOPY_DEMO", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	cmd := tui.RemoveWorktreeCmdForTestCtx(ctx, "/tmp/canopy-test-not-a-worktree")
	msg := cmd()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("removeWorktreeCmd took %s with pre-cancelled ctx — ctx not propagated", elapsed)
	}

	// Route through Update so we don't need to peek at unexported msg fields.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(msg)
	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "prune failed") {
		t.Errorf("notice = %q, want 'prune failed …' (cancelled ctx must surface as a prune error)", notice)
	}
}

// TestCreateWorktreeCmd_CancelledCtxShortCircuits is the same propagation
// check on createWorktreeCmd. We must run in a real git repo (otherwise the
// validator/short-circuits run first); we set repoRoot to the current package
// dir which is itself inside the canopy git worktree.
func TestCreateWorktreeCmd_CancelledCtxShortCircuits(t *testing.T) {
	t.Setenv("CANOPY_DEMO", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	// A branch name we'd never legitimately use; if the ctx is honored the
	// command never actually runs git, so no branch is created.
	cmd := tui.CreateWorktreeCmdForTestCtx(ctx, t.TempDir(), "feat/canopy-ctx-test", "main")
	msg := cmd()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("createWorktreeCmd took %s with pre-cancelled ctx — ctx not propagated", elapsed)
	}

	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(msg)
	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "create failed") {
		t.Errorf("notice = %q, want 'create failed …' (cancelled ctx must surface as a create error)", notice)
	}
}

// TestFormatGitError_StripsExitStatusPrefix is the core of #25: a wrapped
// exec error whose Error() reads "exit status 128: fatal: …" should be
// reduced to just the stderr tail.
func TestFormatGitError_StripsExitStatusPrefix(t *testing.T) {
	err := errors.New("exit status 128")
	got := tui.FormatGitErrorForTest(err, []byte("  fatal: not a working tree\n"))
	if got == nil {
		t.Fatal("FormatGitErrorForTest returned nil; want non-nil error")
	}
	if got.Error() != "fatal: not a working tree" {
		t.Errorf("formatted = %q, want 'fatal: not a working tree' (whitespace trimmed, exit-status prefix dropped)", got.Error())
	}
	if strings.Contains(got.Error(), "exit status") {
		t.Errorf("formatted = %q still contains 'exit status' prefix", got.Error())
	}
}

// TestFormatGitError_EmptyOutputReturnsOriginal: when stderr is empty there's
// nothing to substitute, so the original err is preserved.
func TestFormatGitError_EmptyOutputReturnsOriginal(t *testing.T) {
	err := errors.New("exit status 1")
	for _, out := range [][]byte{nil, {}, []byte("   \n  ")} {
		got := tui.FormatGitErrorForTest(err, out)
		if got == nil || got.Error() != err.Error() {
			t.Errorf("FormatGitErrorForTest(err, %q) = %v, want original err %q", string(out), got, err.Error())
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
