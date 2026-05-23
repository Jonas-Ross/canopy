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

// armLive seeds worktreePath into the model as not-Live, then transitions
// it to Live via a second UpdateMsg. Returns the updated model and the
// tea.Cmd from the live-transition update — non-nil iff the transition
// kicked the blink tick.
func armLive(t *testing.T, m tea.Model, worktreePath, branch string) (tea.Model, tea.Cmd) {
	t.Helper()
	live := &sessions.Session{ID: branch, Model: "claude-opus-4-7", Cwds: []string{worktreePath}}
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: worktreePath,
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree(worktreePath, branch)},
	}))
	m, cmd := m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: worktreePath,
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree(worktreePath, branch),
			Live:     live,
		},
	}))
	return m, cmd
}

func TestBlinkTick_StartsOnFirstLive(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	if tui.BlinkRunningOf(m) {
		t.Fatalf("pre-condition: blinkRunning = true on fresh model")
	}

	m, cmd := armLive(t, m, "/repo/wt-a", "feat/a")

	if !tui.BlinkRunningOf(m) {
		t.Errorf("blinkRunning = false after first Live worktree, want true")
	}
	if !tui.BlinkPhaseOf(m) {
		t.Errorf("blinkPhase = false after first Live worktree, want true (start on bright phase)")
	}
	if cmd == nil {
		t.Errorf("Update returned nil cmd after first Live worktree, want a tick cmd")
	}
}

func TestBlinkTick_DoesNotDoubleStart(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m, firstCmd := armLive(t, m, "/repo/wt-a", "feat/a")
	if firstCmd == nil {
		t.Fatalf("pre-condition: first armLive must return a tick cmd")
	}

	// A second Live worktree arriving while the tick is in flight must NOT
	// kick another tick — overlapping ticks would double the blink rate.
	_, secondCmd := armLive(t, m, "/repo/wt-b", "feat/b")
	if secondCmd != nil {
		t.Errorf("Update returned non-nil cmd after second Live worktree, want nil (tick already running)")
	}
}

func TestBlinkTick_TogglesPhaseAndReschedules(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m, _ = armLive(t, m, "/repo/wt-a", "feat/a")
	if !tui.BlinkPhaseOf(m) {
		t.Fatalf("pre-condition: blinkPhase = false after arm, want true")
	}

	m, cmd := m.Update(tui.MakeBlinkTickMsg())
	if tui.BlinkPhaseOf(m) {
		t.Errorf("blinkPhase = true after one tick, want false (tick must toggle)")
	}
	if cmd == nil {
		t.Errorf("tick returned nil cmd while a Live worktree exists, want reschedule")
	}

	m, cmd = m.Update(tui.MakeBlinkTickMsg())
	if !tui.BlinkPhaseOf(m) {
		t.Errorf("blinkPhase = false after two ticks, want true (tick must toggle again)")
	}
	if cmd == nil {
		t.Errorf("tick returned nil cmd while a Live worktree exists, want reschedule")
	}
}

func TestBlinkTick_StopsWhenNoLive(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m, _ = armLive(t, m, "/repo/wt-a", "feat/a")

	// Drop Live by sending an UpdateMsg with State.Live = nil.
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "feat/a")},
	}))

	m, cmd := m.Update(tui.MakeBlinkTickMsg())
	if cmd != nil {
		t.Errorf("tick returned non-nil cmd with no Live worktrees, want nil (must self-terminate)")
	}
	if tui.BlinkRunningOf(m) {
		t.Errorf("blinkRunning = true after self-termination, want false")
	}
}

// TestBlinkTick_RestartsAfterIdle verifies the tick lifecycle can complete
// (start → all Live drops → stop) and then restart cleanly when a Live
// worktree reappears. Guards against a regression where blinkRunning would
// stay true after self-termination, suppressing the next start.
func TestBlinkTick_RestartsAfterIdle(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m, _ = armLive(t, m, "/repo/wt-a", "feat/a")
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "feat/a")},
	}))
	m, _ = m.Update(tui.MakeBlinkTickMsg()) // self-terminates
	if tui.BlinkRunningOf(m) {
		t.Fatalf("pre-condition: blinkRunning must be false after self-termination")
	}

	_, cmd := armLive(t, m, "/repo/wt-b", "feat/b")
	if cmd == nil {
		t.Errorf("Update returned nil cmd on Live reappearing after idle, want a fresh tick cmd")
	}
}

// TestBlinkTick_FreshLiveAfterDropResetsToBright covers the rapid drop+rearm
// case: while the tick chain is still rolling (blinkRunning=true), a previous
// tick may have already toggled blinkPhase to false. If the only Live then
// drops and a new Live arrives before the next tick fires, that new Live's
// first paint must be bright — the design invariant "the glyph doesn't render
// dim before the first tick fires" must hold for every arrival, not just
// cold-start ones.
func TestBlinkTick_FreshLiveAfterDropResetsToBright(t *testing.T) {
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m, _ = armLive(t, m, "/repo/wt-a", "feat/a")
	// Fire one tick to drive blinkPhase to false (mid-cycle).
	m, _ = m.Update(tui.MakeBlinkTickMsg())
	if tui.BlinkPhaseOf(m) {
		t.Fatalf("pre-condition: blinkPhase = true after one tick, want false")
	}

	// Drop wt-a's Live but keep blinkRunning=true (in-flight tick).
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "feat/a")},
	}))
	if !tui.BlinkRunningOf(m) {
		t.Fatalf("pre-condition: blinkRunning must remain true while a tick is still pending")
	}

	// New Live arrives. Its first paint must be bright.
	m, _ = armLive(t, m, "/repo/wt-b", "feat/b")

	if !tui.BlinkPhaseOf(m) {
		t.Errorf("blinkPhase = false on freshly-arrived Live worktree, want true (first paint must be bright per design invariant)")
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
