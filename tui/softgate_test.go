package tui_test

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/tui"
)

// withDemoMode flips CANOPY_DEMO=1 for the duration of f, restoring the
// prior value on exit.
func withDemoMode(t *testing.T, f func()) {
	t.Helper()
	prior, had := os.LookupEnv("CANOPY_DEMO")
	if err := os.Setenv("CANOPY_DEMO", "1"); err != nil {
		t.Fatalf("set env: %v", err)
	}
	defer func() {
		if had {
			_ = os.Setenv("CANOPY_DEMO", prior)
		} else {
			_ = os.Unsetenv("CANOPY_DEMO")
		}
	}()
	f()
}

// callTeaMsg executes a tea.Cmd with a timeout and returns the produced
// tea.Msg. Fails the test if the timeout elapses — soft-gates are expected
// to return well under the timeout, so a hang means the gate didn't fire.
func callTeaMsg(t *testing.T, c tea.Cmd, timeout time.Duration) tea.Msg {
	t.Helper()
	done := make(chan tea.Msg, 1)
	go func() { done <- c() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(timeout):
		t.Fatalf("tea.Cmd did not complete within %s — soft-gate likely not firing", timeout)
		return nil
	}
}

func TestSoftGate_RemoveWorktree_DemoModeReturnsSuccess(t *testing.T) {
	withDemoMode(t, func() {
		cmd := tui.RemoveWorktreeCmdForTest("/definitely/not/a/real/worktree")
		msg := callTeaMsg(t, cmd, 200*time.Millisecond)
		m := tui.NewModel(&fakeRefresher{})
		m, _ = m.Update(msg)
		notice := stripANSI(tui.NoticeOf(m))
		if !strings.Contains(notice, "pruned") {
			t.Errorf("soft-gated remove notice = %q, want 'pruned …'", notice)
		}
	})
}

func TestSoftGate_KillProcs_DemoModeReturnsCountSuccess(t *testing.T) {
	withDemoMode(t, func() {
		cmd := tui.KillProcsCmdForTest([]int{99991, 99992, 99993})
		msg := callTeaMsg(t, cmd, 200*time.Millisecond)
		m := tui.NewModel(&fakeRefresher{})
		m, _ = m.Update(msg)
		notice := stripANSI(tui.NoticeOf(m))
		if !strings.Contains(notice, "sent SIGTERM to 3 procs") {
			t.Errorf("soft-gated kill notice = %q, want '3 procs'", notice)
		}
	})
}

func TestSoftGate_OpenURL_DemoModeSilentSuccess(t *testing.T) {
	withDemoMode(t, func() {
		cmd := tui.OpenURLCmdForTest("https://example.invalid/nope")
		msg := callTeaMsg(t, cmd, 200*time.Millisecond)
		m := tui.NewModel(&fakeRefresher{})
		m, _ = m.Update(msg)
		// prOpenedMsg with no error dismisses the "opening…" placeholder.
		// Without that placeholder set, the notice should remain empty.
		if notice := tui.NoticeOf(m); notice != "" {
			t.Errorf("soft-gated openURL notice = %q, want empty", notice)
		}
	})
}

func TestSoftGate_NotSetByDefault(t *testing.T) {
	// Belt-and-braces: ensure the test env doesn't have CANOPY_DEMO
	// leaking from an outer process. If it does, downstream tests in
	// this package that assume non-demo behaviour would silently pass.
	if v, ok := os.LookupEnv("CANOPY_DEMO"); ok && v == "1" {
		t.Fatalf("CANOPY_DEMO is set in the test process environment (%q); other tests may misbehave", v)
	}
}
