package tui_test

import (
	"strings"
	"testing"

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
