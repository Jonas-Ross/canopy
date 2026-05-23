package analytics

import (
	"testing"
	"time"
)

func TestPerWorktree_groupsByCwd(t *testing.T) {
	store := newTestStore(t, []sessionSpec{
		// Two sessions under feat+auth worktree (1h each = 2h total).
		{id: "a1", model: "claude-opus-4-7",
			started: day(5, 20, 10), updated: day(5, 20, 11),
			cwd: "/repo/.worktrees/feat+auth"},
		{id: "a2", model: "claude-opus-4-7",
			started: day(5, 22, 14), updated: day(5, 22, 15),
			cwd: "/repo/.worktrees/feat+auth"},
		// One session in repo root (1h).
		{id: "r1", model: "claude-sonnet-4-6",
			started: day(5, 21, 9), updated: day(5, 21, 10),
			cwd: "/repo"},
	})

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := PerWorktree(store, "", since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 worktree rows, got %d: %+v", len(got), got)
	}
	// Sorted DESC by TotalTime: feat+auth (2h) before /repo (1h).
	if got[0].Path != "/repo/.worktrees/feat+auth" {
		t.Errorf("first row should be feat+auth, got %q", got[0].Path)
	}
	if got[0].SessionCount != 2 {
		t.Errorf("feat+auth SessionCount: got %d, want 2", got[0].SessionCount)
	}
	if got[0].TotalTime != 2*time.Hour {
		t.Errorf("feat+auth TotalTime: got %v, want 2h", got[0].TotalTime)
	}
	// LastSeen should be the max UpdatedAt for that worktree.
	wantLastSeen := day(5, 22, 15)
	if !got[0].LastSeen.Equal(wantLastSeen) {
		t.Errorf("feat+auth LastSeen: got %v, want %v", got[0].LastSeen, wantLastSeen)
	}
	if got[1].Path != "/repo" {
		t.Errorf("second row should be /repo, got %q", got[1].Path)
	}
	if got[1].TotalTime != time.Hour {
		t.Errorf("/repo TotalTime: got %v, want 1h", got[1].TotalTime)
	}
}

func TestPerWorktree_repoRootFilter(t *testing.T) {
	store := newTestStore(t, []sessionSpec{
		{id: "in", model: "claude-opus-4-7",
			started: day(5, 22, 10), updated: day(5, 22, 11),
			cwd: "/myrepo/worktrees/feat"},
		{id: "out", model: "claude-opus-4-7",
			started: day(5, 22, 10), updated: day(5, 22, 11),
			cwd: "/otherrepo"},
	})

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := PerWorktree(store, "/myrepo", since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "/myrepo/worktrees/feat" {
		t.Errorf("expected only /myrepo/worktrees/feat, got %+v", got)
	}
}
