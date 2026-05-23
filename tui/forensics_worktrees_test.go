package tui_test

import (
	"strings"
	"testing"

	"github.com/jonasross/canopy/tui"
)

// TestForensicsWorktreesView_golden pins the worktrees sub-view golden frame.
func TestForensicsWorktreesView_golden(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewWorktrees, scenarioAnalytics())
	assertGolden(t, "forensics_worktrees", frame(m))
}

// TestForensicsWorktreesView_headerPresent verifies column headers render.
func TestForensicsWorktreesView_headerPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewWorktrees, scenarioAnalytics())
	view := stripANSI(m.View())
	for _, hdr := range []string{"worktree", "sessions", "total time", "last seen"} {
		if !strings.Contains(view, hdr) {
			t.Errorf("worktrees view missing header %q; view=\n%s", hdr, view)
		}
	}
}

// TestForensicsWorktreesView_footerTotals verifies the totals row is rendered.
func TestForensicsWorktreesView_footerTotals(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewWorktrees, scenarioAnalytics())
	view := stripANSI(m.View())
	if !strings.Contains(view, "total") {
		t.Errorf("worktrees view missing 'total' footer row; view=\n%s", view)
	}
}

// TestForensicsWorktreesView_worktreeNamesPresent verifies worktree basenames appear.
func TestForensicsWorktreesView_worktreeNamesPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewWorktrees, scenarioAnalytics())
	view := stripANSI(m.View())
	for _, wt := range []string{"feat+auth", "chore+deps"} {
		if !strings.Contains(view, wt) {
			t.Errorf("worktrees view missing worktree name %q; view=\n%s", wt, view)
		}
	}
}
