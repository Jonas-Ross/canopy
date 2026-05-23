package tui_test

import (
	"errors"
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

// emptySnap returns a Snapshot representing a successful load of a
// genuinely empty store: GeneratedAt is set so the Update handler
// flips analyticsLoaded=true (distinct from "not yet loaded"), but
// none of the four data fields carry rows.
func emptySnap() analytics.Snapshot {
	return analytics.Snapshot{GeneratedAt: goldenClock}
}

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

	// Initial view is viewTools (zero value).
	if got := tui.ActiveView(m); got != tui.ViewTools {
		t.Fatalf("initial forensics view = %v, want ViewTools", got)
	}

	// Digit keys jump directly.
	for _, tc := range []struct {
		key  rune
		want tui.View
	}{
		{'2', tui.ViewWorktrees},
		{'3', tui.ViewSpend},
		{'4', tui.ViewSessions},
		{'1', tui.ViewTools},
	} {
		m, _ = m.Update(sendKey(tc.key))
		if got := tui.ActiveView(m); got != tc.want {
			t.Errorf("after key %c: view = %v, want %v", tc.key, got, tc.want)
		}
	}

	// h/l cycle with wrap-around.
	// Start at ViewTools (1), h wraps to ViewSessions (4).
	m, _ = m.Update(sendKey('h'))
	if got := tui.ActiveView(m); got != tui.ViewSessions {
		t.Errorf("h from ViewTools: view = %v, want ViewSessions (wrap-around)", got)
	}
	// l from ViewSessions (4) → ViewTools (1).
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewTools {
		t.Errorf("l from ViewSessions: view = %v, want ViewTools (wrap-around)", got)
	}
	// l forward: ViewTools → ViewWorktrees → ViewSpend → ViewSessions.
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewWorktrees {
		t.Errorf("l from ViewTools: view = %v, want ViewWorktrees", got)
	}
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewSpend {
		t.Errorf("l from ViewWorktrees: view = %v, want ViewSpend", got)
	}
	m, _ = m.Update(sendKey('l'))
	if got := tui.ActiveView(m); got != tui.ViewSessions {
		t.Errorf("l from ViewSpend: view = %v, want ViewSessions", got)
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
	// Navigate away from the default tools view to worktrees.
	m, _ = m.Update(sendKey('2'))
	if got := tui.ActiveView(m); got != tui.ViewWorktrees {
		t.Fatalf("pre-condition: view = %v, want ViewWorktrees", got)
	}
	// Leave and return.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // → ops
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // → forensics
	if got := tui.ActiveView(m); got != tui.ViewWorktrees {
		t.Errorf("sub-view after round-trip = %v, want ViewWorktrees (should persist)", got)
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

// TestForensicsLoadingState verifies the forensics body shows a "loading"
// placeholder while the async analytics.Build is in flight (before any
// AnalyticsLoadedMsg arrives). Distinct from the "no sessions yet" case.
func TestForensicsLoadingState(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, frozenNow())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // → forensics tab; no snapshot delivered yet
	view := stripANSI(m.View())
	if !strings.Contains(view, "loading") {
		t.Errorf("pre-load forensics view should show loading placeholder; view=%q", view)
	}
	if strings.Contains(view, "no sessions yet") {
		t.Errorf("pre-load view must not claim emptiness; view=%q", view)
	}
}

// TestForensicsLoadErrorPersistsAcrossKeypress verifies that a failed
// analytics load surfaces a sticky error in the forensics body — not
// just a transient notice — so the user can still see it after the
// next keypress clears m.notice. Without persistence, a failed first
// load is indistinguishable from a genuinely empty store.
func TestForensicsLoadErrorPersistsAcrossKeypress(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, frozenNow())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // → forensics tab
	loadErr := errors.New("store closed")
	m, _ = m.Update(tui.AnalyticsLoadedMsg{Err: loadErr})

	// Sanity: the error appears as a transient notice on first render.
	if view := stripANSI(m.View()); !strings.Contains(view, "store closed") {
		t.Fatalf("expected initial error in view; view=%q", view)
	}

	// Press 'h' (a no-op sub-tab nav key) — handleKey clears m.notice.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})

	view := stripANSI(m.View())
	if strings.Contains(view, "no sessions yet") {
		t.Errorf("failed load should not look like empty store; view=%q", view)
	}
	if !strings.Contains(view, "store closed") {
		t.Errorf("error should persist in body after keypress; view=%q", view)
	}
}

// TestForensicsSuccessfulLoadClearsError verifies a subsequent
// successful load wipes the persisted analyticsErr so the body's sticky
// error state goes away once data is available. The transient notice is
// already cleared by the retry keypress in the real flow.
func TestForensicsSuccessfulLoadClearsError(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, frozenNow())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tui.AnalyticsLoadedMsg{Err: errors.New("transient")})
	// 'r' triggers the retry; handleKey clears the transient m.notice.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = m.Update(tui.AnalyticsLoadedMsg{Snapshot: populatedSnap()})

	view := stripANSI(m.View())
	if strings.Contains(view, "analytics unavailable") {
		t.Errorf("successful load should clear sticky error; view=%q", view)
	}
	if strings.Contains(view, "transient") {
		t.Errorf("error string should be gone after success; view=%q", view)
	}
}

// TestForensicsFooterShowsNotice verifies that an async-op notice set
// on m.notice (e.g. a worktree prune result that arrived while the user
// was on the forensics tab) is rendered in the footer, mirroring the
// operational footer's behavior.
func TestForensicsFooterShowsNotice(t *testing.T) {
	m := buildForensicsModel(t, populatedSnap(), 140, 30)
	m = tui.SetNotice(m, "pruned feat/auth")
	view := stripANSI(m.View())
	if !strings.Contains(view, "pruned feat/auth") {
		t.Errorf("forensics footer dropped notice; view=%q", view)
	}
}

// --- Golden tests ---

// TestForensicsEmptyState_golden pins the empty-state frame.
func TestForensicsEmptyState_golden(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 140, 30)
	assertGolden(t, "forensics_empty", frame(m))
}

// TestForensicsSubTabBar_golden pins the default (tools) sub-tab golden frame.
// The visual highlight of other sub-tabs is tested via TestForensicsSubTabBar_activeHighlight.
func TestForensicsSubTabBar_golden(t *testing.T) {
	m := buildForensicsModel(t, emptySnap(), 140, 30)
	m, _ = m.Update(sendKey('1'))
	assertGolden(t, "forensics_subtab_tools", frame(m))
}
