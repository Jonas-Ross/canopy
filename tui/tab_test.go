package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/tui"
)

// TestTab_InitialTabIsOperational verifies that the initial tab is
// tabOperational (zero value).
func TestTab_InitialTabIsOperational(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	if got := tui.ActiveTab(m); got != tui.TabOperational {
		t.Errorf("initial tab = %v, want TabOperational", got)
	}
}

// TestTab_TabKeyCyclesOpsToForensics verifies that pressing Tab in normal
// mode switches the active tab from ops to forensics.
func TestTab_TabKeyCyclesOpsToForensics(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := tui.ActiveTab(m); got != tui.TabForensics {
		t.Errorf("after Tab, active tab = %v, want TabForensics", got)
	}
}

// TestTab_TabKeyWrapsForensicsBackToOps verifies the full cycle:
// ops → forensics → ops.
func TestTab_TabKeyWrapsForensicsBackToOps(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := tui.ActiveTab(m); got != tui.TabOperational {
		t.Errorf("after two Tabs, active tab = %v, want TabOperational", got)
	}
}

// TestTab_ForensicsViewRendersPlaceholder verifies that the forensics tab
// renders a view containing "forensics" in the title and a footer hint.
func TestTab_ForensicsViewRendersPlaceholder(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})

	view := stripANSI(m.View())
	lower := strings.ToLower(view)
	if !strings.Contains(lower, "forensics") {
		t.Errorf("forensics view missing 'forensics' text; view=%q", view)
	}
}

// TestTab_ForensicsViewDoesNotShowWorktreeList verifies that switching to
// forensics hides the operational worktree list.
func TestTab_ForensicsViewDoesNotShowWorktreeList(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "feat/some-unique-branch")},
	})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})

	view := stripANSI(m.View())
	if strings.Contains(view, "feat/some-unique-branch") {
		t.Errorf("forensics view unexpectedly contains worktree branch name; view=%q", view)
	}
}

// TestTab_OpsViewRestoredAfterTabBack verifies that switching back to ops
// shows the worktree list again.
func TestTab_OpsViewRestoredAfterTabBack(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "feat/some-unique-branch")},
	})
	// Go to forensics then back.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})

	view := stripANSI(m.View())
	if !strings.Contains(view, "feat/some-unique-branch") {
		t.Errorf("ops view after two Tabs missing worktree branch; view=%q", view)
	}
}

// TestTab_FormTabDoesNotCycleTopLevelTab verifies that Tab in modeNewWorktree
// cycles form fields rather than switching the top-level tab.
func TestTab_FormTabDoesNotCycleTopLevelTab(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/main",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/main", "main")},
	})
	// Enter the new-worktree form.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if got := tui.ModeOf(m); got != tui.ModeNewWorktreeForTest {
		t.Fatalf("pre-condition: mode = %d, want modeNewWorktree", got)
	}
	// Tab should cycle form focus, NOT switch to forensics tab.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := tui.ActiveTab(m); got != tui.TabOperational {
		t.Errorf("form Tab switched top-level tab to %v, want TabOperational", got)
	}
	// Form focus should have advanced.
	if got := tui.NewFormFocusOf(m); got != 1 {
		t.Errorf("form focus = %d after Tab in form mode, want 1 (base input)", got)
	}
}
