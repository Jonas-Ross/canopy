package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/sessions"
	"github.com/jonasross/canopy/tui"
)

// Tests for notice lifecycle and small invariants of the Update path.
//
// Background: commit 2cb6680 removed the auto-clear timer on notices and
// made the lifetime "until the next keypress". These guard that invariant
// so a future regression that re-introduces timers (or a different scheme)
// surfaces here rather than only in manual testing.

func TestNotice_ClearsOnNextKeypress(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "main")},
	}))

	// `p` on a worktree with no PR sets the "no PR for …" notice.
	m, _ = m.Update(sendKey('p'))
	if notice := stripANSI(tui.NoticeOf(m)); !strings.Contains(notice, "no PR for") {
		t.Fatalf("pre-condition: notice = %q, want 'no PR for …'", notice)
	}

	// Any subsequent keypress must clear it before the new handler runs.
	m, _ = m.Update(sendKey('j'))
	if notice := tui.NoticeOf(m); notice != "" {
		t.Errorf("notice = %q after next keypress, want empty", notice)
	}
}

func TestNotice_ReplacedByActionOnSameKeypress(t *testing.T) {
	// `p` clears any prior notice, then handleOpenPR sets a new one.
	// Verify both sequencing properties in a single transition.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "main")},
	}))

	// First `p`: sets "no PR for main".
	m, _ = m.Update(sendKey('p'))
	first := stripANSI(tui.NoticeOf(m))

	// Second `p`: should clear and re-set the same notice (idempotent).
	m, _ = m.Update(sendKey('p'))
	second := stripANSI(tui.NoticeOf(m))

	if first != second {
		t.Errorf("notice unstable across repeated `p`: first=%q second=%q", first, second)
	}
	if !strings.Contains(second, "no PR for") {
		t.Errorf("notice = %q, want 'no PR for …'", second)
	}
}

func TestEmptyList_RendersNoWorktreesPlaceholder(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	view := stripANSI(m.View())
	if !strings.Contains(view, "(no worktrees)") {
		t.Errorf("empty-list view missing '(no worktrees)' placeholder:\n%s", view)
	}
}

func TestFilter_NoMatchesRendersPlaceholder(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "main")},
	}))

	m, _ = m.Update(sendKey('/'))
	for _, r := range "doesnotmatch" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))

	view := stripANSI(m.View())
	if strings.Contains(view, "main") {
		t.Errorf("filter 'doesnotmatch' should hide branch 'main'; view:\n%s", view)
	}
	if !strings.Contains(view, "(no worktrees)") {
		t.Errorf("filter with no matches missing '(no worktrees)' placeholder:\n%s", view)
	}
}

func TestPrune_NoFocus_NoMode(t *testing.T) {
	// startPrune does nothing when there's no focused state. The model
	// should remain in normal mode.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(sendKey('d'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("d with no focus, mode = %d, want %d", got, tui.ModeNormalForTest)
	}
}

func TestEscInNormalMode_ClearsCommittedFilter(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "main")},
	}))

	m, _ = m.Update(sendKey('/'))
	for _, r := range "main" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))
	if got := tui.FilterValue(m); got != "main" {
		t.Fatalf("pre-condition: filter = %q, want 'main'", got)
	}

	// Esc from normal mode (filter committed, not active) should clear it.
	m, _ = m.Update(sendSpecialKey(tea.KeyEsc))
	if got := tui.FilterValue(m); got != "" {
		t.Errorf("filter = %q after esc in normal mode, want empty", got)
	}
}

// armPulse seeds worktreePath into the model and transitions Live nil→non-nil
// at the current clk, arming a pulse. Returns the updated model.
func armPulse(t *testing.T, m tea.Model, worktreePath, branch string) tea.Model {
	t.Helper()
	live := &sessions.Session{ID: branch, Model: "claude-opus-4-7", Cwds: []string{worktreePath}}
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: worktreePath,
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree(worktreePath, branch)},
	}))
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: worktreePath,
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree(worktreePath, branch),
			Live:     live,
		},
	}))
	return m
}

func TestPulseExpiredMsg_ClearsPulseState(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m = armPulse(t, m, "/repo/wt-b", "feat/b")

	if got := tui.PulsePathOf(m); got != "/repo/wt-b" {
		t.Fatalf("pre-condition: pulsePath = %q, want /repo/wt-b", got)
	}
	until := tui.PulseUntilOf(m)
	if until.IsZero() {
		t.Fatalf("pre-condition: pulseUntil is zero, want non-zero")
	}

	// Advance past the deadline before firing the tick — the handler only
	// clears state when the timer is actually expired (see fresher-pulse test).
	clk = until.Add(time.Millisecond)
	m, _ = m.Update(tui.MakePulseExpiredMsg())

	if got := tui.PulsePathOf(m); got != "" {
		t.Errorf("pulsePath = %q after pulseExpiredMsg, want empty (handler must clear)", got)
	}
	if got := tui.PulseUntilOf(m); !got.IsZero() {
		t.Errorf("pulseUntil = %v after pulseExpiredMsg, want zero time (handler must clear)", got)
	}
}

// Bursty live updates schedule overlapping tea.Tick timers. When the older
// tick fires while a fresher pulse is still active, pulseExpiredMsg must
// be a no-op — otherwise the highlight duration becomes non-deterministic
// under rapid updates.
func TestPulseExpiredMsg_StaleTimerLeavesFresherPulseAlone(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m = armPulse(t, m, "/repo/wt-a", "feat/a")
	firstUntil := tui.PulseUntilOf(m)

	// Advance time and arm a second, fresher pulse on a different worktree.
	clk = clk.Add(200 * time.Millisecond)
	m = armPulse(t, m, "/repo/wt-b", "feat/b")
	secondUntil := tui.PulseUntilOf(m)
	if !secondUntil.After(firstUntil) {
		t.Fatalf("pre-condition: second pulseUntil (%v) must be later than first (%v)", secondUntil, firstUntil)
	}

	// Advance the clock to the first pulse's deadline, when its tick fires.
	// The second pulse is still active (secondUntil > firstUntil > clk).
	clk = firstUntil
	m, _ = m.Update(tui.MakePulseExpiredMsg())

	if got := tui.PulsePathOf(m); got != "/repo/wt-b" {
		t.Errorf("pulsePath = %q after stale tick, want /repo/wt-b (newer pulse must survive)", got)
	}
	if got := tui.PulseUntilOf(m); !got.Equal(secondUntil) {
		t.Errorf("pulseUntil = %v after stale tick, want %v (must not advance/clear newer pulse)", got, secondUntil)
	}
}

func TestDetailPane_SubagentSessionsFiltered(t *testing.T) {
	// Two sessions on the focused worktree: one top-level and one sidechain.
	// The detail pane should only render the top-level one in the Sessions
	// block (per the bug fix that introduced sidechain filtering).
	top := &sessions.Session{
		ID: "11111", Model: "claude-opus-4-7",
		Cwds: []string{"/repo/wt-a"},
	}
	side := &sessions.Session{
		ID: "11111#sub", Model: "claude-sub-4-5",
		Cwds: []string{"/repo/wt-a"}, IsSidechain: true,
		ParentSessionID: "11111",
	}

	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/a"),
			Live:     top,
			Recent:   []*sessions.Session{top, side},
		},
	}))

	view := stripANSI(m.View())
	if !strings.Contains(view, "claude-opus-4-7") {
		t.Errorf("detail pane missing top-level session model; view:\n%s", view)
	}
	if strings.Contains(view, "claude-sub-4-5") {
		t.Errorf("detail pane includes sidechain session model — should be filtered; view:\n%s", view)
	}
}
