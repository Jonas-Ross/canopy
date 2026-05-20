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
	view := stripANSI(m.View())
	if !strings.Contains(view, "branch name required") {
		t.Errorf("View does not display the validation error — form footer must surface m.notice; view:\n%s", view)
	}
}

// The validation notice must survive keystrokes while the form is open so
// the user can read it while retyping.
func TestNewForm_ValidationErrorPersistsAcrossKeystrokes(t *testing.T) {
	m := seedNewForm(t)
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))
	if got := stripANSI(tui.NoticeOf(m)); !strings.Contains(got, "branch name required") {
		t.Fatalf("pre-condition: notice = %q, want 'branch name required' before subsequent keystroke", got)
	}
	m, _ = m.Update(sendKey('f'))

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "branch name required") {
		t.Errorf("notice = %q after one keystroke, want still 'branch name required' (must not be cleared in form mode)", notice)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "branch name required") {
		t.Errorf("View no longer displays the validation error after a keystroke; view:\n%s", view)
	}
}

// Esc must clear the validation error so the next open starts clean.
func TestNewForm_EscClearsValidationError(t *testing.T) {
	m := seedNewForm(t)
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))
	m, _ = m.Update(sendSpecialKey(tea.KeyEsc))
	if notice := stripANSI(tui.NoticeOf(m)); notice != "" {
		t.Errorf("notice after Esc = %q, want empty (form cancel must clear validation error)", notice)
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
	if got := tui.NewFormFocusOf(m); got != 0 {
		t.Fatalf("pre-condition: newFormFocus = %d, want 0 (branch)", got)
	}
	m, _ = m.Update(sendSpecialKey(tea.KeyTab))
	if got := tui.NewFormFocusOf(m); got != 1 {
		t.Fatalf("after Tab, newFormFocus = %d, want 1 (base)", got)
	}
	m = typeStr(m, "develop")
	if branch := tui.NewFormBranchValueOf(m); branch != "" {
		t.Errorf("branch input value = %q after Tab + typing, want empty", branch)
	}
	// baseIn is seeded with "main" and the textinput cursor sits at end,
	// so typed runes append. The exact concatenation pins both the seed
	// survived and the typed runes hit the base input — a footer-substring
	// check couldn't distinguish that from a Tab no-op.
	if base := tui.NewFormBaseValueOf(m); base != "maindevelop" {
		t.Errorf("base input value = %q after Tab + typing 'develop', want %q", base, "maindevelop")
	}

	m, _ = m.Update(sendSpecialKey(tea.KeyTab))
	if got := tui.NewFormFocusOf(m); got != 0 {
		t.Errorf("after second Tab, newFormFocus = %d, want 0 (cycle back to branch)", got)
	}
}

func TestNewForm_ShiftTabAlsoCycles(t *testing.T) {
	m := seedNewForm(t)
	if got := tui.NewFormFocusOf(m); got != 0 {
		t.Fatalf("pre-condition: newFormFocus = %d, want 0 (branch)", got)
	}
	m, _ = m.Update(sendSpecialKey(tea.KeyShiftTab))
	if got := tui.NewFormFocusOf(m); got != 1 {
		t.Errorf("after Shift-Tab, newFormFocus = %d, want 1 (base)", got)
	}
	m = typeStr(m, "trunk")
	if branch := tui.NewFormBranchValueOf(m); branch != "" {
		t.Errorf("branch input value = %q after Shift-Tab + typing, want empty", branch)
	}
	if base := tui.NewFormBaseValueOf(m); base != "maintrunk" {
		t.Errorf("base input value = %q after Shift-Tab + typing 'trunk', want %q", base, "maintrunk")
	}
}
