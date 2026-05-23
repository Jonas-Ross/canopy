package analytics

import (
	"testing"
	"time"
)

func TestRecentSessions_topNByUpdatedAt(t *testing.T) {
	store := newTestStore(t, []sessionSpec{
		{id: "old", model: "claude-opus-4-7", started: day(5, 1, 10), updated: day(5, 1, 11),
			prompts: 1, tools: map[string]int{"Bash": 1}},
		{id: "newer", model: "claude-opus-4-7", started: day(5, 22, 10), updated: day(5, 22, 11),
			prompts: 3, tools: map[string]int{"Bash": 5, "Read": 2},
			cwd: "/repo/.worktrees/feat+auth"},
		{id: "newest", model: "claude-sonnet-4-6", started: day(5, 23, 10), updated: day(5, 23, 11),
			prompts: 2, tools: map[string]int{"Read": 1},
			cwd: "/repo"},
	})

	got, err := RecentSessions(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0].ID != "newest" || got[1].ID != "newer" {
		t.Errorf("order: %+v", []string{got[0].ID, got[1].ID})
	}
	if got[1].ToolCalls != 7 {
		t.Errorf("newer row ToolCalls: got %d, want 7", got[1].ToolCalls)
	}
	// prompts: 1 initial + 3 extra = 4 total user lines (not tool_results)
	if got[1].Prompts != 4 {
		t.Errorf("newer row Prompts: got %d, want 4", got[1].Prompts)
	}
	if got[1].Worktree != "/repo/.worktrees/feat+auth" {
		t.Errorf("worktree should be the deepest cwd: %q", got[1].Worktree)
	}
	if got[1].Duration != time.Hour {
		t.Errorf("duration should be UpdatedAt-StartedAt: %v", got[1].Duration)
	}
}

func TestRecentSessions_fewerThanN(t *testing.T) {
	store := newTestStore(t, []sessionSpec{
		{id: "only", model: "claude-opus-4-7", started: day(5, 22, 10), updated: day(5, 22, 11)},
	})
	got, err := RecentSessions(store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("want 1, got %d", len(got))
	}
}

func TestRecentSessions_nonPositiveLimitReturnsEmpty(t *testing.T) {
	// Guards against panic from all[:n] / make(..., 0, n) on negative n,
	// and treats n == 0 as a degenerate "give me nothing" request.
	store := newTestStore(t, []sessionSpec{
		{id: "s1", model: "claude-opus-4-7", started: day(5, 22, 10), updated: day(5, 22, 11)},
	})
	for _, n := range []int{0, -1, -100} {
		got, err := RecentSessions(store, n)
		if err != nil {
			t.Errorf("n=%d: unexpected err %v", n, err)
		}
		if len(got) != 0 {
			t.Errorf("n=%d: want empty, got %d rows", n, len(got))
		}
	}
}
