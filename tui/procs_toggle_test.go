package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/tui"
)

// seedWithProcs returns a model with one worktree carrying `n` non-claude
// procs — enough to trigger the soft cap.
func seedWithProcs(n int) tea.Model {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	procsList := make([]procs.Process, n)
	for i := 0; i < n; i++ {
		procsList[i] = procs.Process{Pid: 1000 + i, Command: "zsh"}
	}
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "main"),
			Procs:    procsList,
		},
	}))
	return m
}

func TestProcsToggle_DefaultsCollapsed(t *testing.T) {
	m := seedWithProcs(10)
	if tui.ProcsExpandedFor(m, "/repo/wt-a") {
		t.Errorf("default state should be collapsed")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "+5 more (P)") {
		t.Errorf("collapsed view should show '+5 more (P)':\n%s", view)
	}
}

func TestProcsToggle_PExpandsAndRecollapses(t *testing.T) {
	m := seedWithProcs(10)

	// Press P → expanded.
	m, _ = m.Update(sendKey('P'))
	if !tui.ProcsExpandedFor(m, "/repo/wt-a") {
		t.Errorf("after first P, state should be expanded")
	}
	view := stripANSI(m.View())
	if strings.Contains(view, "more") {
		t.Errorf("expanded view should not contain 'more':\n%s", view)
	}

	// Press P again → collapsed.
	m, _ = m.Update(sendKey('P'))
	if tui.ProcsExpandedFor(m, "/repo/wt-a") {
		t.Errorf("after second P, state should be collapsed")
	}
	view = stripANSI(m.View())
	if !strings.Contains(view, "+5 more (P)") {
		t.Errorf("re-collapsed view should show '+5 more (P)':\n%s", view)
	}
}

func TestProcsToggle_PerWorktreeState(t *testing.T) {
	// Two worktrees with heavy proc lists; toggling one must not affect the other.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	mkProcs := func(start int) []procs.Process {
		out := make([]procs.Process, 10)
		for i := 0; i < 10; i++ {
			out[i] = procs.Process{Pid: start + i, Command: "zsh"}
		}
		return out
	}
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-a", "main"),
			Procs:    mkProcs(1000),
		},
	}))
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-b",
		State: aggregator.WorktreeState{
			Worktree: newBaseWorktree("/repo/wt-b", "feat/x"),
			Procs:    mkProcs(2000),
		},
	}))

	// Focus on wt-a (index 0 by default). Press P.
	m, _ = m.Update(sendKey('P'))
	if !tui.ProcsExpandedFor(m, "/repo/wt-a") {
		t.Errorf("wt-a should be expanded after P")
	}
	if tui.ProcsExpandedFor(m, "/repo/wt-b") {
		t.Errorf("wt-b should remain collapsed (P toggles focused worktree only)")
	}
}

func TestProcsToggle_FooterMentionsP(t *testing.T) {
	m := seedWithProcs(10)
	view := stripANSI(m.View())
	if !strings.Contains(view, "P procs") && !strings.Contains(view, "P:procs") {
		t.Errorf("footer should mention the P keybind:\n%s", view)
	}
}
