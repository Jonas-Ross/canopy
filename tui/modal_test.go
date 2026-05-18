package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/tui"
)

// Tests for the modal-mode keypress dispatchers in tui/tui.go. The 'y'
// (confirm-prune, confirm-kill) happy paths are covered by golden_test.go;
// these focus on the cancellation paths (n / N / Esc) that were
// previously asserted only transitively.

// seedPrune builds a model with one focused worktree and enters prune
// confirmation. Returns the mid-modal model.
func seedPrune(t *testing.T) tea.Model {
	t.Helper()
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/a"),
		},
	}))
	m, _ = m.Update(sendKey('d'))
	if got := tui.ModeOf(m); got != tui.ModeConfirmPruneForTest {
		t.Fatalf("after 'd', mode = %d, want %d", got, tui.ModeConfirmPruneForTest)
	}
	return m
}

// seedKill builds a model with one focused worktree that has running procs
// and enters kill confirmation.
func seedKill(t *testing.T) tea.Model {
	t.Helper()
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/a"),
			Procs:    []procs.Process{{Pid: 1234, Cwd: "/repo/wt-a", Command: "claude"}},
		},
	}))
	m, _ = m.Update(sendKey('K'))
	if got := tui.ModeOf(m); got != tui.ModeConfirmKillForTest {
		t.Fatalf("after 'K', mode = %d, want %d", got, tui.ModeConfirmKillForTest)
	}
	return m
}

func TestConfirmPrune_LowerNCancels(t *testing.T) {
	m := seedPrune(t)
	m, _ = m.Update(sendKey('n'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("after 'n', mode = %d, want %d (normal)", got, tui.ModeNormalForTest)
	}
}

func TestConfirmPrune_UpperNCancels(t *testing.T) {
	m := seedPrune(t)
	m, _ = m.Update(sendKey('N'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("after 'N', mode = %d, want %d", got, tui.ModeNormalForTest)
	}
}

func TestConfirmPrune_EscCancels(t *testing.T) {
	m := seedPrune(t)
	m, _ = m.Update(sendSpecialKey(tea.KeyEsc))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("after esc, mode = %d, want %d", got, tui.ModeNormalForTest)
	}
}

func TestConfirmPrune_UnrelatedKeyStaysInMode(t *testing.T) {
	m := seedPrune(t)
	m, _ = m.Update(sendKey('x'))
	if got := tui.ModeOf(m); got != tui.ModeConfirmPruneForTest {
		t.Errorf("after unrelated 'x', mode = %d, want still %d", got, tui.ModeConfirmPruneForTest)
	}
}

func TestConfirmKill_LowerNCancels(t *testing.T) {
	m := seedKill(t)
	m, _ = m.Update(sendKey('n'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("after 'n', mode = %d, want %d", got, tui.ModeNormalForTest)
	}
}

func TestConfirmKill_UpperNCancels(t *testing.T) {
	m := seedKill(t)
	m, _ = m.Update(sendKey('N'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("after 'N', mode = %d, want %d", got, tui.ModeNormalForTest)
	}
}

func TestConfirmKill_EscCancels(t *testing.T) {
	m := seedKill(t)
	m, _ = m.Update(sendSpecialKey(tea.KeyEsc))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("after esc, mode = %d, want %d", got, tui.ModeNormalForTest)
	}
}

func TestKillKey_NoProcs_ShowsNoticeNoMode(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/a"),
			// Procs intentionally nil.
		},
	}))

	m, _ = m.Update(sendKey('K'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("K with no procs put model in mode %d; should stay normal", got)
	}
	if notice := stripANSI(tui.NoticeOf(m)); !strings.Contains(notice, "no processes to kill") {
		t.Errorf("notice = %q, want 'no processes to kill'", notice)
	}
}

func TestOpenPR_NoWorktreeFocused_ShowsNotice(t *testing.T) {
	// Empty model, no Updates → focusedState returns false.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(sendKey('p'))
	if notice := stripANSI(tui.NoticeOf(m)); !strings.Contains(notice, "no worktree focused") {
		t.Errorf("notice = %q, want 'no worktree focused'", notice)
	}
}
