package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/tui"
)

// Tests for the filter+focus interaction surfaced by the audit. When a
// filter hides the currently-focused worktree:
//   - focusedState() must report no focus (so d/p/K early-out).
//   - On filter commit (Enter), focus snaps to the first visible row.

// seedTwo seeds a model with two worktrees on branches "main" and "feat/x"
// so a filter of "feat" leaves only feat/x visible.
func seedTwo(t *testing.T) tea.Model {
	t.Helper()
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/main",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/main", "main")},
	}))
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-x",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-x", "feat/x")},
	}))
	return m
}

// applyFilter types s into the filter and commits with Enter.
func applyFilter(t *testing.T, m tea.Model, s string) tea.Model {
	t.Helper()
	m, _ = m.Update(sendKey('/'))
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))
	return m
}

func TestFilterCommit_SnapsFocusToFirstVisible(t *testing.T) {
	m := seedTwo(t)
	// Focus is on index 0 (main) by default.
	if got := tui.FocusIndex(m); got != 0 {
		t.Fatalf("pre-condition: focus = %d, want 0", got)
	}
	// Filter for "feat" — only feat/x (index 1) matches.
	m = applyFilter(t, m, "feat")
	if got := tui.FocusIndex(m); got != 1 {
		t.Errorf("focus = %d after filter snap, want 1 (the first visible row)", got)
	}
}

func TestFilterCommit_FocusStaysWhenStillVisible(t *testing.T) {
	m := seedTwo(t)
	// Move focus to feat/x first.
	m, _ = m.Update(sendKey('j'))
	if got := tui.FocusIndex(m); got != 1 {
		t.Fatalf("pre-condition: focus = %d, want 1", got)
	}
	// Filter for "feat" — focused row stays visible.
	m = applyFilter(t, m, "feat")
	if got := tui.FocusIndex(m); got != 1 {
		t.Errorf("focus = %d after filter, want 1 (no snap when focus is visible)", got)
	}
}

func TestPruneOnHiddenFocus_NoModeChange(t *testing.T) {
	m := seedTwo(t)
	// Filter to "main" then move focus to feat/x (which is now hidden).
	// We have to apply the filter first to put it in committed state,
	// then move focus directly to the hidden row index to simulate the
	// case where snap couldn't fully recover (e.g., later worktree
	// arrived under a stale filter).
	m = applyFilter(t, m, "main")
	// Force focus to feat/x (index 1, hidden under "main" filter) by
	// pressing j past the visible rows. moveFocus walks the raw list.
	m, _ = m.Update(sendKey('j'))
	if got := tui.FocusIndex(m); got != 1 {
		t.Fatalf("pre-condition: focus = %d, want 1 (hidden under filter)", got)
	}
	// Press d — should NOT enter modeConfirmPrune because focusedState is
	// filter-aware and reports no focus.
	m, _ = m.Update(sendKey('d'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("mode = %d after d on hidden focus, want %d (normal — startPrune early-out)", got, tui.ModeNormalForTest)
	}
}

func TestOpenPROnHiddenFocus_NoticesMissingFocus(t *testing.T) {
	m := seedTwo(t)
	m = applyFilter(t, m, "main")
	m, _ = m.Update(sendKey('j')) // focus → feat/x (hidden)
	m, _ = m.Update(sendKey('p'))
	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "no worktree focused") {
		t.Errorf("notice = %q, want 'no worktree focused' (focusedState filter-aware)", notice)
	}
}

func TestKillOnHiddenFocus_NoModeChange(t *testing.T) {
	m := seedTwo(t)
	m = applyFilter(t, m, "main")
	m, _ = m.Update(sendKey('j'))
	m, _ = m.Update(sendKey('K'))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("mode = %d after K on hidden focus, want %d (normal)", got, tui.ModeNormalForTest)
	}
}
