package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/analytics"
	"github.com/jonasross/canopy/sessions"
)

// TestForensicsSubTabBar_activeHighlight asserts that the active sub-tab label
// carries the tabActive style (cyan + bold SGR codes) while inactive labels do not.
// This is an ANSI-aware test that captures what the stripped goldens cannot:
// visual differences in highlight via style-only changes.
func TestForensicsSubTabBar_activeHighlight(t *testing.T) {
	for _, tc := range []struct {
		key          rune
		wantActive   string // label that should be wrapped in tabActive
		wantInactive []string // labels that should NOT be wrapped in tabActive
	}{
		{'1', "tools", []string{"worktrees", "spend", "sessions"}},
		{'2', "worktrees", []string{"tools", "spend", "sessions"}},
		{'3', "spend", []string{"tools", "worktrees", "sessions"}},
		{'4', "sessions", []string{"tools", "worktrees", "spend"}},
	} {
		m := buildForensicsModel(t, emptySnap(), 140, 30)
		tm, _ := m.Update(sendKey(tc.key))
		mModel := tm.(Model)

		// Render the view with full ANSI codes (not stripped).
		view := mModel.View()

		// The active label should be wrapped in tabActive style.
		wantActive := tabActive.Render(tc.wantActive)
		if !strings.Contains(view, wantActive) {
			t.Errorf("key %c: expected %q (active label with SGR codes) in view, not found", tc.key, wantActive)
		}

		// Inactive labels should NOT be wrapped in tabActive style.
		for _, inactiveLabel := range tc.wantInactive {
			wantInactiveActive := tabActive.Render(inactiveLabel)
			if strings.Contains(view, wantInactiveActive) {
				t.Errorf("key %c: label %q should NOT be wrapped in tabActive, but found %q in view",
					tc.key, inactiveLabel, wantInactiveActive)
			}
		}
	}
}

// Helper: buildForensicsModel constructs a Model on the forensics tab.
// Duplicated from forensics_test.go but in package tui so it can access
// the unexported tabActive style.
func buildForensicsModel(t *testing.T, snap analytics.Snapshot, width, height int) Model {
	t.Helper()
	tm := NewModel(&fakeRefresherInternal{})
	m, ok := tm.(Model)
	if !ok {
		t.Fatalf("NewModel returned non-Model type: %T", tm)
	}
	tm = SetNow(m, frozenNowInternal())
	m = tm.(Model)
	tm, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = tm.(Model)
	// Switch to forensics tab.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = tm.(Model)
	// Inject the analytics snapshot directly.
	tm, _ = m.Update(AnalyticsLoadedMsg{Snapshot: snap})
	m = tm.(Model)
	return m
}

// Helper: emptySnap returns a zero-value analytics.Snapshot with no sessions.
func emptySnap() analytics.Snapshot { return analytics.Snapshot{} }

// Helper: sendKey builds a tea.KeyMsg for a single rune.
func sendKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// --- Internal test fixtures (duplicated from tui_test suite) ---

// fakeRefresherInternal is a minimal Refresher for internal package tui tests.
type fakeRefresherInternal struct{}

func (f *fakeRefresherInternal) Refresh()              {}
func (f *fakeRefresherInternal) SessionStore() *sessions.Store { return nil }

// goldenClockInternal matches the frozen time used in tui_test.
var goldenClockInternal = time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

func frozenNowInternal() func() time.Time {
	return func() time.Time { return goldenClockInternal }
}
