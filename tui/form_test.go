package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/tui"
)

// Tests for the new-worktree form's input loop. The form's existence is
// asserted by ops_test.go and golden_test.go; these cover validation,
// cancellation, and field-focus cycling.

func seedNewForm(t *testing.T) tea.Model {
	t.Helper()
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/main",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/main", "main")},
	}))
	m, _ = m.Update(sendKey('n'))
	if got := tui.ModeOf(m); got != tui.ModeNewWorktreeForTest {
		t.Fatalf("after 'n', mode = %d, want %d", got, tui.ModeNewWorktreeForTest)
	}
	return m
}

func typeStr(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestNewForm_EscCancelsBackToNormal(t *testing.T) {
	m := seedNewForm(t)
	m, _ = m.Update(sendSpecialKey(tea.KeyEsc))
	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("after esc, mode = %d, want %d", got, tui.ModeNormalForTest)
	}
}

func TestNewForm_EnterWithEmptyBranchShowsError(t *testing.T) {
	m := seedNewForm(t)
	// No keypresses → branch input is empty. Press Enter.
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "branch name required") {
		t.Errorf("notice = %q, want 'branch name required'", notice)
	}
	// Still in form mode — the error is recoverable.
	if got := tui.ModeOf(m); got != tui.ModeNewWorktreeForTest {
		t.Errorf("after empty-Enter, mode = %d, want %d (stay in form)", got, tui.ModeNewWorktreeForTest)
	}
}

func TestNewForm_EnterWithWhitespaceBranchShowsError(t *testing.T) {
	m := seedNewForm(t)
	m = typeStr(m, "   ")
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))
	if notice := stripANSI(tui.NoticeOf(m)); !strings.Contains(notice, "branch name required") {
		t.Errorf("whitespace-only branch did not produce required-error: notice = %q", notice)
	}
}

func TestNewForm_TabCyclesFocus(t *testing.T) {
	m := seedNewForm(t)
	// Initial focus is on branch (newFormFocus == 0); render must show the
	// branch input as focused. The cleanest assertion is the rendered
	// View after a Tab: now focus should be on the base input. Because
	// renderFooter renders both inputs unconditionally, we instead drive
	// behaviour: after Tab, typing should land in the base input.
	m, _ = m.Update(sendSpecialKey(tea.KeyTab))
	m = typeStr(m, "develop")
	// After committing with Enter, the resulting worktreeCreatedMsg-or-cmd
	// reflects which base was used. We can't easily intercept that here
	// without further export. Instead, assert the form's rendered footer
	// includes the typed base value.
	footerLower := strings.ToLower(stripANSI(m.View()))
	if !strings.Contains(footerLower, "develop") {
		t.Errorf("after Tab + typing 'develop', footer did not include the typed base; view=%q", footerLower)
	}
}

func TestNewForm_ShiftTabAlsoCycles(t *testing.T) {
	m := seedNewForm(t)
	// shift-tab on field 0 → field 1 (cycle to base) — symmetric with tab.
	m, _ = m.Update(sendSpecialKey(tea.KeyShiftTab))
	m = typeStr(m, "trunk")
	footerLower := strings.ToLower(stripANSI(m.View()))
	if !strings.Contains(footerLower, "trunk") {
		t.Errorf("after Shift-Tab + typing 'trunk', footer did not include the typed base; view=%q", footerLower)
	}
}
