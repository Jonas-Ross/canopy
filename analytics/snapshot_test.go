package analytics

import (
	"testing"
	"time"
)

func TestBuild_populatesAllSubfields(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, []sessionSpec{
		{id: "s1", model: "claude-opus-4-7",
			started: day(5, 22, 10), updated: day(5, 22, 11),
			usage: usage{Input: 500, Output: 250},
			tools: map[string]int{"Bash": 3},
			cwd:   "/repo"},
		{id: "s2", model: "claude-sonnet-4-6",
			started: day(5, 21, 9), updated: day(5, 21, 10),
			usage: usage{Input: 300, Output: 150},
			cwd:   "/repo/.worktrees/feat"},
	})

	snap, err := Build(store, now)
	if err != nil {
		t.Fatal(err)
	}

	// GeneratedAt must equal now.
	if !snap.GeneratedAt.Equal(now) {
		t.Errorf("GeneratedAt: got %v, want %v", snap.GeneratedAt, now)
	}
	// WindowStart must be 29 days before now's UTC midnight, giving an
	// inclusive 30-day range when rendered.
	nowUTC := now.UTC()
	endDay := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	wantStart := endDay.AddDate(0, 0, -29)
	if !snap.WindowStart.Equal(wantStart) {
		t.Errorf("WindowStart: got %v, want %v", snap.WindowStart, wantStart)
	}
	if !snap.WindowEnd.Equal(now) {
		t.Errorf("WindowEnd: got %v, want %v", snap.WindowEnd, now)
	}

	// Days should have 2 entries (one per day).
	if len(snap.Days) != 2 {
		t.Errorf("Days: got %d, want 2", len(snap.Days))
	}
	// Sessions should have both sessions.
	if len(snap.Sessions) != 2 {
		t.Errorf("Sessions: got %d, want 2", len(snap.Sessions))
	}
	// Tools should have at least one entry (Bash from s1).
	if len(snap.Tools) == 0 {
		t.Errorf("Tools: expected at least one entry")
	}
	// Worktrees should have 2 entries (/repo and /repo/.worktrees/feat).
	if len(snap.Worktrees) != 2 {
		t.Errorf("Worktrees: got %d, want 2", len(snap.Worktrees))
	}
}

func TestBuild_emptyStore(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, nil)

	snap, err := Build(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.GeneratedAt.Equal(now) {
		t.Errorf("GeneratedAt: got %v, want %v", snap.GeneratedAt, now)
	}
	if len(snap.Days) != 0 || len(snap.Sessions) != 0 || len(snap.Tools) != 0 || len(snap.Worktrees) != 0 {
		t.Errorf("empty store should produce empty sub-fields: %+v", snap)
	}
}
