package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/analytics"
	"github.com/jonasross/canopy/tui"
)

// buildForensicsModel constructs a Model on the forensics tab, sized to
// (width, height), with a frozen clock and the given analytics snapshot.
func buildForensicsModel(t *testing.T, snap analytics.Snapshot, width, height int) tea.Model {
	t.Helper()
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, frozenNow())
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	// Switch to forensics tab.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	// Inject the analytics snapshot directly.
	m, _ = m.Update(tui.AnalyticsLoadedMsg{Snapshot: snap})
	return m
}

// emptySnap returns a zero-value analytics.Snapshot with no sessions.
func emptySnap() analytics.Snapshot { return analytics.Snapshot{} }

// populatedSnap returns a Snapshot with a non-empty Sessions slice so
// the body renders the stub rather than the empty-state placeholder.
func populatedSnap() analytics.Snapshot {
	now := goldenClock
	return analytics.Snapshot{
		GeneratedAt: now,
		WindowStart: now.Add(-30 * 24 * time.Hour),
		WindowEnd:   now,
		Sessions: []analytics.SessionSummary{
			{ID: "s1", Model: "claude-opus-4-7", Worktree: "/repo/wt-a", StartedAt: now.Add(-1 * time.Hour), UpdatedAt: now, Duration: time.Hour, Prompts: 5, ToolCalls: 10},
		},
	}
}

// --- Sub-tab navigation ---

// TestForensicsSubTabNav verifies digit, h/l navigation on the forensics tab.
func TestForensicsSubTabNav(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 140, 30)

	// Initial view is viewSpend (zero value).
	if got := tui.ActiveView(m); got != tui.ViewSpend {
		t.Fatalf("initial forensics view = %v, want ViewSpend", got)
	}

	// Digit keys jump directly.
	for _, tc := range []struct {
		key  rune
		want tui.View
	}{
		{'2', tui.ViewSessions},
		{'3', tui.ViewTools},
		{'4', tui.ViewWorktrees},
		{'1', tui.ViewSpend},
	} {
		m, _ = m.Update(sendKey(tc.key))
		if got := tui.ActiveView(m); got != tc.want {
			t.Errorf("after key %c: view = %v, want %v", tc.key, got, tc.want)
		}
	}

	// h/l cycle with wrap-around.
	// Start at ViewSpend (1), h wraps to ViewWorktrees (4).
	m, _ = m.Update(sendKey('h'))
	if got := tui.ActiveView(m); got != tui.ViewWorktrees {
		t.Errorf("h from ViewSpend: view = %v, want ViewWorktrees (wrap-around)", got)
	}
	// l from ViewWorktrees (4) → ViewSpend (1).
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewSpend {
		t.Errorf("l from ViewWorktrees: view = %v, want ViewSpend (wrap-around)", got)
	}
	// l forward: ViewSpend → ViewSessions → ViewTools → ViewWorktrees.
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewSessions {
		t.Errorf("l from ViewSpend: view = %v, want ViewSessions", got)
	}
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewTools {
		t.Errorf("l from ViewSessions: view = %v, want ViewTools", got)
	}
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewWorktrees {
		t.Errorf("l from ViewTools: view = %v, want ViewWorktrees", got)
	}
}

// TestForensicsDigitKeysNoOpOnOpsTab verifies that digit keys 1-4 do NOT
// affect the model when on the operational tab.
func TestForensicsDigitKeysNoOpOnOpsTab(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})

	// Pre-condition: on ops tab.
	if got := tui.ActiveTab(m); got != tui.TabOperational {
		t.Fatalf("pre-condition: expected TabOperational, got %v", got)
	}

	// Press 1-4 on ops tab — should not panic, not change tab.
	for _, r := range []rune{'1', '2', '3', '4'} {
		m, _ = m.Update(sendKey(r))
		if got := tui.ActiveTab(m); got != tui.TabOperational {
			t.Errorf("digit key %c on ops tab changed tab to %v", r, got)
		}
	}
}

// TestForensicsHLKeysNoOpOnOpsTab verifies h/l do not affect the ops tab
// (they are currently unbound there).
func TestForensicsHLKeysNoOpOnOpsTab(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	for _, r := range []rune{'h', 'l'} {
		m, _ = m.Update(sendKey(r))
		if got := tui.ActiveTab(m); got != tui.TabOperational {
			t.Errorf("key %c on ops tab changed tab to %v", r, got)
		}
	}
}

// TestForensicsTabBackToOps verifies Tab on forensics returns to ops.
func TestForensicsTabBackToOps(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 80, 30)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := tui.ActiveTab(m); got != tui.TabOperational {
		t.Errorf("Tab from forensics: tab = %v, want TabOperational", got)
	}
}

// TestForensicsSubTabNotPersistAcrossTabCycle verifies that the sub-tab
// survives a round-trip ops→forensics→ops→forensics (i.e. navigating away
// and back does not reset the selected sub-view).
func TestForensicsSubTabNotPersistAcrossTabCycle(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 80, 30)
	// Navigate to sessions view.
	m, _ = m.Update(sendKey('2'))
	if got := tui.ActiveView(m); got != tui.ViewSessions {
		t.Fatalf("pre-condition: view = %v, want ViewSessions", got)
	}
	// Leave and return.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // → ops
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // → forensics
	if got := tui.ActiveView(m); got != tui.ViewSessions {
		t.Errorf("sub-view after round-trip = %v, want ViewSessions (should persist)", got)
	}
}

// --- Sub-tab bar rendering ---

// TestForensicsSubTabBarLabels verifies all four labels appear in the view.
func TestForensicsSubTabBarLabels(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 140, 30)
	view := stripANSI(m.View())
	for _, label := range []string{"spend", "sessions", "tools", "worktrees"} {
		if !strings.Contains(view, label) {
			t.Errorf("forensics view missing sub-tab label %q; view=%q", label, view)
		}
	}
}

// TestForensicsSubTabBarNoDigitPrefixes verifies that digit prefixes do NOT
// appear in the sub-tab bar labels.
func TestForensicsSubTabBarNoDigitPrefixes(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 140, 30)
	view := stripANSI(m.View())
	for _, bad := range []string{"1.", "2.", "3.", "4.", "1 spend", "2 sessions", "3 tools", "4 worktrees"} {
		if strings.Contains(view, bad) {
			t.Errorf("forensics view contains digit prefix %q in sub-tab bar; view=%q", bad, view)
		}
	}
}

// --- Empty state ---

// TestForensicsEmptyState verifies the empty-state placeholder renders
// when the snapshot has no sessions.
func TestForensicsEmptyState(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 140, 30)
	view := stripANSI(m.View())
	if !strings.Contains(view, "no sessions yet") {
		t.Errorf("empty-state forensics view missing placeholder; view=%q", view)
	}
}

// TestForensicsNonEmptyState verifies the empty-state placeholder is NOT
// rendered when the snapshot has sessions.
func TestForensicsNonEmptyState(t *testing.T) {
	m := buildForensicsModel(t, populatedSnap(), 140, 30)
	view := stripANSI(m.View())
	if strings.Contains(view, "no sessions yet") {
		t.Errorf("non-empty forensics view shows empty-state placeholder; view=%q", view)
	}
}

// --- Golden tests ---

// TestForensicsEmptyState_golden pins the empty-state frame.
func TestForensicsEmptyState_golden(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 140, 30)
	assertGolden(t, "forensics_empty", frame(m))
}

// TestForensicsSubTabBar_golden pins four separate golden frames, one per
// highlighted sub-view. Four files are cheaper to diff than one combined file.
func TestForensicsSubTabBar_golden(t *testing.T) {
	for _, tc := range []struct {
		key    rune
		golden string
	}{
		{'1', "forensics_subtab_spend"},
		{'2', "forensics_subtab_sessions"},
		{'3', "forensics_subtab_tools"},
		{'4', "forensics_subtab_worktrees"},
	} {
		m := buildForensicsModel(t, emptySnap(), 140, 30)
		m, _ = m.Update(sendKey(tc.key))
		assertGolden(t, tc.golden, frame(m))
	}
}
