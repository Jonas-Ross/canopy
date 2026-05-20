package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/sessions"
	"github.com/jonasross/canopy/tui"
)

// helper: build a populated state with one focused worktree.
func newPopulatedModel(t *testing.T) tea.Model {
	t.Helper()
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r/wt-a",
		State: aggregator.WorktreeState{
			Repo: aggregator.Repo{Root: "/r", Name: "r"},
			Worktree: git.Worktree{
				Path: "/r/wt-a", Branch: "feat/a",
				DirtyFiles: 2, Ahead: 1, HasUpstream: true,
				LastCommit: git.Commit{Subject: "test", When: time.Now().Add(-5 * time.Minute)},
			},
			PR: &pr.PR{
				Number: 142, State: pr.PRStateOpen, CIRollup: pr.CISuccess, ReviewState: pr.ReviewApproved,
				URL: "https://example.com/pr/142", Title: "Test PR",
			},
			Procs: []procs.Process{
				{Pid: 1234, Command: "claude", Args: []string{"--model", "opus"}},
			},
			Live: &sessions.Session{ID: "s1", Model: "claude-opus-4-7", UpdatedAt: time.Now()},
		},
	}))
	return m
}

func TestView_PRColumn_VisibleAtWidth100(t *testing.T) {
	m := newPopulatedModel(t)
	view := m.View()
	if !strings.Contains(view, "#142") {
		t.Errorf("View at width=160 missing PR number '#142'; view=%q", view)
	}
}

func TestView_PRColumn_HiddenBelowWidth100(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r/wt-a",
		State: aggregator.WorktreeState{
			Worktree: git.Worktree{Path: "/r/wt-a", Branch: "feat/a"},
			PR:       &pr.PR{Number: 142, URL: "https://x"},
		},
	}))
	view := m.View()
	if strings.Contains(view, "#142") {
		t.Errorf("View at width=80 unexpectedly contains '#142'; view=%q", view)
	}
}

func TestView_ProcsColumn_VisibleAtWidth120(t *testing.T) {
	m := newPopulatedModel(t)
	view := m.View()
	// Procs column rendered: count + '*' marker for claude.
	if !strings.Contains(view, "1*") {
		t.Errorf("View at width=160 missing claude procs indicator '1*'; view=%q", view)
	}
}

func TestView_DetailPane_VisibleAtWidth140(t *testing.T) {
	m := newPopulatedModel(t)
	view := m.View()
	// Detail pane renders these label strings.
	for _, want := range []string{"Sessions", "Processes", "PR", "commit", "Test PR"} {
		if !strings.Contains(view, want) {
			t.Errorf("View at width=160 missing detail pane content %q; view=%q", want, view)
		}
	}
}

func TestView_DetailPane_HiddenAtWidth80(t *testing.T) {
	m := newPopulatedModel(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	view := m.View()
	// "Processes" label is detail-pane-only — should not appear at width=80.
	if strings.Contains(view, "Processes") {
		t.Errorf("View at width=80 unexpectedly shows detail pane (saw 'Processes'); view=%q", view)
	}
}

func TestUpdate_OpenPR_SetsNotice(t *testing.T) {
	m := newPopulatedModel(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	view := m.View()
	// Notice should reference the URL we tried to open.
	if !strings.Contains(view, "example.com/pr/142") {
		t.Errorf("View after pressing p missing PR URL notice; view=%q", view)
	}
}

func TestUpdate_OpenPR_NoPR_ShowsNotice(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r/wt-a",
		State: aggregator.WorktreeState{
			Worktree: git.Worktree{Path: "/r/wt-a", Branch: "feat/a"},
		},
	}))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	view := m.View()
	if !strings.Contains(view, "no PR") {
		t.Errorf("View after pressing p with no PR missing 'no PR' notice; view=%q", view)
	}
}

func TestUpdate_PruneKey_EntersConfirmMode(t *testing.T) {
	m := newPopulatedModel(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	view := m.View()
	if !strings.Contains(view, "prune") {
		t.Errorf("View after pressing d missing prune confirm prompt; view=%q", view)
	}
	// Cancel with 'n'
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	view = m.View()
	if strings.Contains(view, "prune feat/a?") {
		t.Errorf("View after pressing 'n' still shows prune prompt; view=%q", view)
	}
}

// The primary worktree row gets a leading ⌂ marker; non-primary rows
// pad the same slot with spaces so columns stay aligned.
func TestView_PrimaryWorktreeMarker(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r",
		State: aggregator.WorktreeState{
			Repo:     aggregator.Repo{Root: "/r", Name: "r"},
			Worktree: git.Worktree{Path: "/r", Branch: "main", Main: true},
		},
	}))
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r/wt-a",
		State: aggregator.WorktreeState{
			Repo:     aggregator.Repo{Root: "/r", Name: "r"},
			Worktree: git.Worktree{Path: "/r/wt-a", Branch: "feat/a"},
		},
	}))

	view := stripANSI(m.View())
	mainLine := findLineContaining(t, view, "main")
	featLine := findLineContaining(t, view, "feat/a")

	if !strings.Contains(mainLine, "⌂") {
		t.Errorf("primary row missing ⌂ marker: %q", mainLine)
	}
	if strings.Contains(featLine, "⌂") {
		t.Errorf("non-primary row has ⌂ marker (must be primary-only): %q", featLine)
	}
}

// The default help footer hides `d prune` while the primary worktree is
// focused — git itself refuses worktree-remove there, so the affordance
// would be misleading.
func TestView_FooterHidesPruneOnPrimary(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r",
		State: aggregator.WorktreeState{
			Repo:     aggregator.Repo{Root: "/r", Name: "r"},
			Worktree: git.Worktree{Path: "/r", Branch: "main", Main: true},
		},
	}))
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r/wt-a",
		State: aggregator.WorktreeState{
			Repo:     aggregator.Repo{Root: "/r", Name: "r"},
			Worktree: git.Worktree{Path: "/r/wt-a", Branch: "feat/a"},
		},
	}))

	// Focused on primary (index 0) — `d prune` must be absent.
	view := stripANSI(m.View())
	if strings.Contains(view, "d prune") {
		t.Errorf("footer shows 'd prune' with primary focused; want it hidden. view=%q", view)
	}

	// Move focus to the non-primary row — `d prune` must return.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	view = stripANSI(m.View())
	if !strings.Contains(view, "d prune") {
		t.Errorf("footer missing 'd prune' with non-primary focused; want it shown. view=%q", view)
	}
}

func findLineContaining(t *testing.T, s, needle string) string {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, s)
	return ""
}

// Regression: pressing d on the primary worktree must not arm the prune flow.
func TestUpdate_PruneKey_OnMainWorktree_NoConfirm(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r",
		State: aggregator.WorktreeState{
			Repo:     aggregator.Repo{Root: "/r", Name: "r"},
			Worktree: git.Worktree{Path: "/r", Branch: "main", Main: true},
		},
	}))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if got := tui.ModeOf(m); got != tui.ModeNormalForTest {
		t.Errorf("mode = %d after d on main worktree, want %d (normal — prune must be refused)", got, tui.ModeNormalForTest)
	}
	view := m.View()
	if strings.Contains(view, "prune main?") {
		t.Errorf("View after pressing d on main worktree still shows prune confirm prompt; view=%q", view)
	}
	if !strings.Contains(view, "cannot prune") {
		t.Errorf("View after pressing d on main worktree missing 'cannot prune' notice; view=%q", view)
	}
}

func TestUpdate_KillKey_EntersConfirmMode(t *testing.T) {
	m := newPopulatedModel(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	view := m.View()
	if !strings.Contains(view, "SIGTERM") {
		t.Errorf("View after pressing K missing kill confirm prompt; view=%q", view)
	}
}

func TestUpdate_NewWorktreeKey_EntersForm(t *testing.T) {
	m := newPopulatedModel(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	view := m.View()
	if !strings.Contains(view, "new worktree") {
		t.Errorf("View after pressing n missing new-worktree form prompt; view=%q", view)
	}
	if !strings.Contains(view, "branch:") {
		t.Errorf("View missing 'branch:' input label; view=%q", view)
	}
	if !strings.Contains(view, "base:") {
		t.Errorf("View missing 'base:' input label; view=%q", view)
	}
}

func TestView_MergedPRRowIsDimmed(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r/wt-merged",
		State: aggregator.WorktreeState{
			Worktree: git.Worktree{Path: "/r/wt-merged", Branch: "fix/done"},
			PR:       &pr.PR{Number: 138, State: pr.PRStateMerged, URL: "https://x"},
		},
	}))
	view := m.View()
	if !strings.Contains(view, "fix/done") {
		t.Errorf("View missing branch fix/done; view=%q", view)
	}
	if !strings.Contains(view, "⌧") {
		t.Errorf("View missing merged-state glyph '⌧'; view=%q", view)
	}
}

func TestPRStaleIndicator(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/r/wt-a",
		State: aggregator.WorktreeState{
			Worktree: git.Worktree{Path: "/r/wt-a", Branch: "feat/a"},
			PR:       &pr.PR{Number: 143, URL: "https://x"},
			PRStale:  true,
		},
	}))
	view := stripANSI(m.View())
	// Stale renders with a leading '~' before the number.
	if !strings.Contains(view, "~#143") {
		t.Errorf("View missing stale PR indicator '~#143'; view=%q", view)
	}
}
