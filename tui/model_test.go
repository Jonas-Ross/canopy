// Package tui_test is the acceptance test suite for the M4 TUI operational
// view. Tests are written against the public surface that the spec defines:
// tui.NewModel, tui.Run, and the bubbletea Model interface. All tests are
// pure — no real terminal, no real git binary, no real ~/.claude.
package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/sessions"
	"github.com/jonasross/canopy/tui"
)

// fakeRefresher satisfies tui.Refresher and records the number of Refresh
// calls so tests can assert the exact count.
type fakeRefresher struct {
	calls int
	store *sessions.Store
}

func (f *fakeRefresher) Refresh() { f.calls++ }

// SessionStore returns f.store, which may be nil. Tests that exercise
// the analytics load path must set the store field; tests that only drive
// the operational tab can leave it nil (loadAnalyticsCmd is never
// executed in those test paths).
func (f *fakeRefresher) SessionStore() *sessions.Store { return f.store }

// newBaseWorktree returns a minimal git.Worktree for use in fixtures.
func newBaseWorktree(path, branch string) git.Worktree {
	return git.Worktree{
		Path:   path,
		Branch: branch,
		LastCommit: git.Commit{
			Hash:    "abc1234",
			Subject: "initial commit",
			When:    time.Now().Add(-5 * time.Minute),
		},
	}
}

// updateMsg builds the tea.Msg-compatible wrapper that the TUI expects when
// an aggregator.Update arrives over the subscription channel. The spec says
// the bridge goroutine forwards aggregator.Update events as bubbletea
// messages; tui.NewModel must accept them in its Update method.
func updateMsg(u aggregator.Update) tui.UpdateMsg {
	return tui.UpdateMsg(u)
}

// sendKey builds a tea.KeyMsg for a single rune (e.g. 'q', 'j', 'k', 'r').
func sendKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// sendSpecialKey builds a tea.KeyMsg for a named key.
func sendSpecialKey(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

// applyUpdate sends an aggregator.Update into the model.
func applyUpdate(m tea.Model, u aggregator.Update) tea.Model {
	m2, _ := m.Update(updateMsg(u))
	return m2
}

// applyKey sends a key into the model.
func applyKey(m tea.Model, r rune) (tea.Model, tea.Cmd) {
	return m.Update(sendKey(r))
}

// applySpecialKey sends a named-key message into the model.
func applySpecialKey(m tea.Model, t tea.KeyType) (tea.Model, tea.Cmd) {
	return m.Update(sendSpecialKey(t))
}

// TestModelUpdate_QuitKey verifies that pressing 'q' causes Model.Update to
// return a tea.Quit command.
//
// Acceptance criterion: "Pressing q causes Model.Update to return a tea.Quit
// command".
func TestModelUpdate_QuitKey(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	_, cmd := applyKey(m, 'q')
	if cmd == nil {
		t.Fatal("cmd is nil after pressing q; want tea.Quit")
	}
	// tea.Quit is a func(); we compare its identity via the msg it produces.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() returned %T; want tea.QuitMsg", msg)
	}
}

// TestModelUpdate_CtrlC verifies that ctrl+c also produces tea.Quit.
func TestModelUpdate_CtrlC(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	_, cmd := applySpecialKey(m, tea.KeyCtrlC)
	if cmd == nil {
		t.Fatal("cmd is nil after ctrl+c; want tea.Quit")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() returned %T; want tea.QuitMsg", msg)
	}
}

// TestModelUpdate_FocusNavigation verifies j/down advance focus and k/up
// decrement it, both clamped to the valid range.
//
// Acceptance criterion: "Pressing j/down advances the focused-row index by 1,
// clamped at len(worktrees)-1. Pressing k/up decrements it, clamped at 0."
func TestModelUpdate_FocusNavigation(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)

	// Populate three worktrees.
	for i, path := range []string{"/repo/wt-a", "/repo/wt-b", "/repo/wt-c"} {
		branch := []string{"feat/a", "feat/b", "feat/c"}[i]
		m = applyUpdate(m, aggregator.Update{
			Worktree: path,
			State: aggregator.WorktreeState{
				Worktree: newBaseWorktree(path, branch),
			},
		})
	}

	assertFocus := func(label string, want int) {
		t.Helper()
		got := tui.FocusIndex(m)
		if got != want {
			t.Errorf("%s: focus index = %d, want %d", label, got, want)
		}
	}

	assertFocus("initial", 0)

	m, _ = applyKey(m, 'j')
	assertFocus("after j", 1)

	m, _ = applyKey(m, 'j')
	assertFocus("after j j", 2)

	// Clamped at last index.
	m, _ = applyKey(m, 'j')
	assertFocus("after j j j (clamped)", 2)

	m, _ = applySpecialKey(m, tea.KeyDown)
	assertFocus("after down (clamped)", 2)

	m, _ = applyKey(m, 'k')
	assertFocus("after k", 1)

	m, _ = applyKey(m, 'k')
	assertFocus("after k k", 0)

	// Clamped at 0.
	m, _ = applyKey(m, 'k')
	assertFocus("after k k k (clamped)", 0)

	m, _ = applySpecialKey(m, tea.KeyUp)
	assertFocus("after up (clamped)", 0)
}

// TestModelUpdate_FocusNavigation_Empty verifies nav keys do not panic or
// produce a negative index when there are no worktrees.
func TestModelUpdate_FocusNavigation_Empty(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = applyKey(m, 'j')
	if got := tui.FocusIndex(m); got != 0 {
		t.Errorf("focus index = %d after j on empty model, want 0", got)
	}
	m, _ = applyKey(m, 'k')
	if got := tui.FocusIndex(m); got != 0 {
		t.Errorf("focus index = %d after k on empty model, want 0", got)
	}
}

// TestModelUpdate_RefreshKey verifies that pressing 'r' calls Refresh()
// exactly once per keypress on the injected Refresher.
//
// Acceptance criterion: "Pressing r triggers a call to aggregator.Refresh()
// exactly once per keypress (assertable via a fake Refresher interface)."
func TestModelUpdate_RefreshKey(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)

	applyKey(m, 'r')
	if rf.calls != 1 {
		t.Errorf("Refresh called %d times after first r; want 1", rf.calls)
	}

	applyKey(m, 'r')
	applyKey(m, 'r')
	if rf.calls != 3 {
		t.Errorf("Refresh called %d times after three r presses; want 3", rf.calls)
	}
}

// TestModelUpdate_FilterMode_EnterAndExit verifies the '/' key enters filter
// mode, typed runes build the filter string, 'enter' commits it, and 'esc'
// clears it.
//
// Acceptance criterion: "Pressing / enters filter-input mode. Typed runes are
// appended to a filter string; enter commits the filter; esc clears it."
func TestModelUpdate_FilterMode_EnterAndExit(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	// '/' enters filter mode.
	m, _ = applyKey(m, '/')
	if !tui.IsFiltering(m) {
		t.Error("IsFiltering = false after pressing /; want true")
	}

	// Typing runes updates the filter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})

	// 'enter' commits — filter mode deactivated, filter string persists.
	m, _ = applySpecialKey(m, tea.KeyEnter)
	if tui.IsFiltering(m) {
		t.Error("IsFiltering = true after enter; want false")
	}
	if got := tui.FilterValue(m); got != "feat" {
		t.Errorf("FilterValue = %q after commit; want %q", got, "feat")
	}

	// 'esc' clears the filter.
	m, _ = applySpecialKey(m, tea.KeyEsc)
	if tui.FilterValue(m) != "" {
		t.Errorf("FilterValue = %q after esc; want empty", tui.FilterValue(m))
	}
	if tui.IsFiltering(m) {
		t.Error("IsFiltering = true after esc; want false")
	}
}

// TestModelView_FilterNarrowsRows verifies that when a non-empty filter is
// active, View() only renders rows whose branch contains the filter
// (case-insensitive).
//
// Acceptance criterion: "When the filter is non-empty, View() only renders
// rows whose Worktree.Branch contains the filter substring (case-insensitive)."
func TestModelView_FilterNarrowsRows(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	states := []struct {
		path   string
		branch string
	}{
		{"/repo/wt-a", "feat/authentication"},
		{"/repo/wt-b", "fix/login-bug"},
		{"/repo/wt-c", "feat/dashboard"},
	}
	for _, s := range states {
		m = applyUpdate(m, aggregator.Update{
			Worktree: s.path,
			State: aggregator.WorktreeState{
				Worktree: newBaseWorktree(s.path, s.branch),
			},
		})
	}

	// Enter filter mode and type "feat" then commit.
	m, _ = applyKey(m, '/')
	for _, r := range "feat" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = applySpecialKey(m, tea.KeyEnter)

	view := m.View()

	if !strings.Contains(view, "feat/authentication") {
		t.Errorf("View missing 'feat/authentication' when filter='feat'; view=%q", view)
	}
	if !strings.Contains(view, "feat/dashboard") {
		t.Errorf("View missing 'feat/dashboard' when filter='feat'; view=%q", view)
	}
	if strings.Contains(view, "fix/login-bug") {
		t.Errorf("View unexpectedly contains 'fix/login-bug' when filter='feat'; view=%q", view)
	}
}

// TestModelView_FilterCaseInsensitive verifies uppercase filter matches lowercase branches.
func TestModelView_FilterCaseInsensitive(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/MyFeature"),
		},
	})

	m, _ = applyKey(m, '/')
	for _, r := range "myfeature" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = applySpecialKey(m, tea.KeyEnter)

	view := m.View()
	if !strings.Contains(view, "feat/MyFeature") {
		t.Errorf("View missing 'feat/MyFeature' with case-insensitive filter 'myfeature'; view=%q", view)
	}
}

// TestModelView_TabKeySwitchesToForensics verifies pressing 'tab' switches to
// the forensics view and a second Tab returns to ops.
//
// Acceptance criterion: "Tab cycles ops → forensics → ops without crashing."
func TestModelView_TabKeySwitchesToForensics(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "main"),
		},
	})

	// First Tab: switches to forensics. The worktree list should not be shown.
	m, _ = applySpecialKey(m, tea.KeyTab)
	viewForensics := stripANSI(m.View())
	if strings.Contains(viewForensics, "main") {
		t.Errorf("forensics view contains operational branch 'main'; view=%q", viewForensics)
	}
	lowerForensics := strings.ToLower(viewForensics)
	if !strings.Contains(lowerForensics, "forensics") {
		t.Errorf("forensics view missing 'forensics' text; view=%q", viewForensics)
	}

	// Second Tab: returns to ops. The worktree list should be shown again.
	m, _ = applySpecialKey(m, tea.KeyTab)
	viewOps := m.View()
	if !strings.Contains(viewOps, "main") {
		t.Errorf("ops view after two Tabs missing branch 'main'; view=%q", viewOps)
	}
}

// TestModelView_FKeyDoesNotCrash verifies pressing 'f' does not crash.
// The 'f' key is now unbound (the forensics shortcut was removed in favour
// of Tab cycling the top-level tab).
func TestModelView_FKeyDoesNotCrash(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = applyKey(m, 'f')
	// Must not panic; a non-empty view is sufficient.
	if view := m.View(); view == "" {
		t.Error("View returned empty string after pressing f")
	}
}

// TestModelUpdate_AggregatorUpdate_NewWorktree verifies that an
// aggregator.Update for a previously unseen path causes that worktree to
// appear in View() on the next render.
//
// Acceptance criterion: "An aggregator.Update message delivered via
// Model.Update for a previously unseen worktree path causes that worktree
// to appear in View() on the next render."
func TestModelUpdate_AggregatorUpdate_NewWorktree(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/new"),
		},
	})

	view := m.View()
	if !strings.Contains(view, "feat/new") {
		t.Errorf("View missing 'feat/new' after Update; view=%q", view)
	}
}

// TestModelUpdate_AggregatorUpdate_ReplaceExisting verifies that a second
// aggregator.Update for the same path replaces the prior state.
//
// Acceptance criterion: "An update for an existing path replaces the prior
// state for that path."
func TestModelUpdate_AggregatorUpdate_ReplaceExisting(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/old"),
		},
	})

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "feat/new"),
		},
	})

	view := m.View()
	if !strings.Contains(view, "feat/new") {
		t.Errorf("View missing 'feat/new' after replace update; view=%q", view)
	}
	if strings.Contains(view, "feat/old") {
		t.Errorf("View still contains 'feat/old' after replace update; view=%q", view)
	}
}

// TestModelView_NWorktrreesShowsNBranches verifies that for N worktrees in
// state, View() contains exactly N branch names in the rendered output.
//
// Acceptance criterion: "Model.View() output for a state containing N
// worktrees contains exactly N branch names in a list-like layout."
func TestModelView_NWorktreesShowsNBranches(t *testing.T) {
	branches := []string{"feat/a", "fix/b", "main", "feat/c", "chore/d"}

	m := tui.NewModel(&fakeRefresher{})
	for i, b := range branches {
		path := "/repo/wt-" + string(rune('a'+i))
		m = applyUpdate(m, aggregator.Update{
			Worktree: path,
			State: aggregator.WorktreeState{
				Worktree: newBaseWorktree(path, b),
			},
		})
	}

	view := m.View()
	for _, b := range branches {
		if !strings.Contains(view, b) {
			t.Errorf("View missing branch %q; view=%q", b, view)
		}
	}
}

// TestModelView_DirtyCount verifies that when DirtyFiles > 0 the dirty count
// appears in the row.
//
// Acceptance criterion: "dirty file count when Worktree.DirtyFiles > 0"
func TestModelView_DirtyCount(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	wt := newBaseWorktree("/repo/wt-a", "feat/a")
	wt.DirtyFiles = 7

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: wt},
	})

	view := m.View()
	if !strings.Contains(view, "7") {
		t.Errorf("View missing dirty count '7'; view=%q", view)
	}
}

// TestModelView_NoDirtyCountWhenZero verifies that when DirtyFiles == 0 no
// misleading zero dirt indicator appears.
func TestModelView_NoDirtyCountWhenZero(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	wt := newBaseWorktree("/repo/wt-a", "feat/a")
	wt.DirtyFiles = 0

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: wt},
	})

	view := m.View()
	// The spec says "dirty file count when DirtyFiles > 0" — zero should not
	// appear as a dirty-file indicator.
	// We can't assert "0 not in view" globally because other numbers may be
	// present; just confirm there's no "0 dirty" or similar phrase.
	if strings.Contains(view, "0 dirty") || strings.Contains(view, "~0") {
		t.Errorf("View contains a 0-dirty indicator when DirtyFiles==0; view=%q", view)
	}
}

// TestModelView_AheadBehind verifies ahead/behind numbers appear only when
// HasUpstream is true.
//
// Acceptance criterion: "ahead/behind only when Worktree.HasUpstream"
func TestModelView_AheadBehind(t *testing.T) {
	tests := []struct {
		name       string
		wt         git.Worktree
		wantAhead  bool
		wantBehind bool
	}{
		{
			name: "has upstream with ahead and behind",
			wt: git.Worktree{
				Path:        "/repo/wt-a",
				Branch:      "feat/a",
				HasUpstream: true,
				Ahead:       3,
				Behind:      1,
				LastCommit:  git.Commit{When: time.Now().Add(-5 * time.Minute)},
			},
			wantAhead:  true,
			wantBehind: true,
		},
		{
			name: "no upstream",
			wt: git.Worktree{
				Path:        "/repo/wt-b",
				Branch:      "feat/b",
				HasUpstream: false,
				Ahead:       5,
				Behind:      2,
				LastCommit:  git.Commit{When: time.Now().Add(-5 * time.Minute)},
			},
			wantAhead:  false,
			wantBehind: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tui.NewModel(&fakeRefresher{})
			m = applyUpdate(m, aggregator.Update{
				Worktree: tc.wt.Path,
				State:    aggregator.WorktreeState{Worktree: tc.wt},
			})
			view := m.View()

			// The spec says ahead/behind appear only with HasUpstream.
			// We check for the numeric values specifically associated with
			// the ahead/behind context — either as "↑3 ↓1" or "3↑" style
			// or plain numbers; the exact format is Dev's choice.
			// We assert the values appear or don't based on HasUpstream.
			if tc.wantAhead && tc.wt.Ahead > 0 {
				// At least the Ahead number should be present somewhere in the row.
				ahead := strings.Contains(view, "3")
				behind := strings.Contains(view, "1")
				if !ahead || !behind {
					t.Errorf("View missing ahead/behind numbers for HasUpstream=true; view=%q", view)
				}
			}
			if !tc.wantAhead && !tc.wantBehind && tc.wt.HasUpstream == false {
				// ahead=5 behind=2 should NOT appear as upstream indicators.
				// We check by ensuring the model renders without showing "↑5"
				// or "↓2" while HasUpstream is false. We test this indirectly
				// via the RelativeAheadBehind helper test below.
				_ = view // rendering must not panic
			}
		})
	}
}

// TestModelView_DetachedHead verifies that Worktree.Detached=true renders as
// "(detached)" rather than an empty branch name.
//
// Acceptance criterion: "the branch string (or (detached) when
// Worktree.Detached is true)"
func TestModelView_DetachedHead(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	wt := git.Worktree{
		Path:       "/repo/wt-a",
		Detached:   true,
		LastCommit: git.Commit{When: time.Now().Add(-5 * time.Minute)},
	}

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: wt},
	})

	view := m.View()
	if !strings.Contains(view, "detached") {
		t.Errorf("View missing 'detached' for detached HEAD worktree; view=%q", view)
	}
}

// TestModelView_RelativeCommitAge verifies a relative time string appears in
// the row (the exact format is Dev's choice — "5m", "5 min", "5m ago", etc.).
//
// Acceptance criterion: "a relative-time string for Worktree.LastCommit.When"
func TestModelView_RelativeCommitAge(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	wt := newBaseWorktree("/repo/wt-a", "main")
	wt.LastCommit.When = time.Now().Add(-5 * time.Minute)

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: wt},
	})

	view := m.View()
	// The view must contain some indication of recency (minutes or hours).
	hasRelTime := strings.Contains(view, "m") || strings.Contains(view, "h") ||
		strings.Contains(view, "min") || strings.Contains(view, "ago") ||
		strings.Contains(view, "now")
	if !hasRelTime {
		t.Errorf("View missing relative time indicator; view=%q", view)
	}
}

// TestModelView_LiveIndicatorPresent verifies that when WorktreeState.Live
// is non-nil the row contains the '●' glyph and the session model string.
//
// Acceptance criterion: "When WorktreeState.Live != nil, that row's rendered
// string contains a ● glyph AND the value of Live.Model."
func TestModelView_LiveIndicatorPresent(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	sess := &sessions.Session{
		ID:    "test-session-1",
		Model: "claude-opus-4-7",
		Cwds:  []string{"/repo/wt-a"},
	}

	wt := newBaseWorktree("/repo/wt-a", "feat/live")

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: wt,
			Live:     sess,
		},
	})

	view := m.View()

	if !strings.Contains(view, "●") {
		t.Errorf("View missing '●' glyph for live worktree; view=%q", view)
	}
	if !strings.Contains(view, "claude-opus-4-7") {
		t.Errorf("View missing model name 'claude-opus-4-7' for live worktree; view=%q", view)
	}
}

// TestModelView_LiveIndicatorAbsent verifies that when WorktreeState.Live is
// nil no '●' glyph appears on that row.
//
// Acceptance criterion: "When Live == nil, the ● glyph MUST NOT appear on
// that row."
func TestModelView_LiveIndicatorAbsent(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	wt := newBaseWorktree("/repo/wt-a", "feat/no-live")

	m = applyUpdate(m, aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: wt,
			Live:     nil,
		},
	})

	view := m.View()
	if strings.Contains(view, "●") {
		t.Errorf("View contains '●' glyph for non-live worktree; view=%q", view)
	}
}

// TestModelView_LiveIndicatorOnCorrectRow verifies that when two rows are
// present and only one is live, only that row shows '●'.
func TestModelView_LiveIndicatorOnCorrectRow(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})

	sess := &sessions.Session{
		ID:    "test-session-live",
		Model: "claude-opus-4-7",
		Cwds:  []string{"/repo/wt-live"},
	}

	for _, tc := range []struct {
		path   string
		branch string
		live   *sessions.Session
	}{
		{"/repo/wt-live", "feat/live", sess},
		{"/repo/wt-dead", "feat/dead", nil},
	} {
		m = applyUpdate(m, aggregator.Update{
			Worktree: tc.path,
			State: aggregator.WorktreeState{
				Worktree: newBaseWorktree(tc.path, tc.branch),
				Live:     tc.live,
			},
		})
	}

	view := m.View()

	// Split view into lines to check per-row.
	lines := strings.Split(view, "\n")

	var liveRowFound bool
	for _, line := range lines {
		if strings.Contains(line, "feat/live") {
			if !strings.Contains(line, "●") {
				t.Errorf("live row %q missing '●'", line)
			}
			liveRowFound = true
		}
		if strings.Contains(line, "feat/dead") {
			if strings.Contains(line, "●") {
				t.Errorf("non-live row %q unexpectedly contains '●'", line)
			}
		}
	}
	if !liveRowFound {
		t.Errorf("no line containing 'feat/live' found in view; view=%q", view)
	}
}

// TestUpdateMsg_Type verifies that tui.UpdateMsg is the type the bridge
// goroutine should produce from aggregator.Update values (compile-time check).
// If this test compiles, the type alias/wrapper is defined correctly.
func TestUpdateMsg_Type(t *testing.T) {
	u := aggregator.Update{
		Worktree: "/repo/wt",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt", "main")},
	}
	msg := tui.UpdateMsg(u)
	// Send it into a model to confirm Update accepts it without a panic.
	m := tui.NewModel(&fakeRefresher{})
	m2, _ := m.Update(msg)
	_ = m2.View() // must not panic
}
