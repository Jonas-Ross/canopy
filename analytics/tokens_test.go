package analytics

import (
	"testing"
	"time"
)

func TestTokensByDay_groupsByDayAndModel(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, []sessionSpec{
		// Two opus sessions on 5/22, one sonnet on 5/21, one opus
		// outside the 30-day window (4/20).
		{id: "s1", model: "claude-opus-4-7", started: day(5, 22, 10), updated: day(5, 22, 11),
			usage: usage{Input: 1000, Output: 500, CacheRead: 2000}},
		{id: "s2", model: "claude-opus-4-7", started: day(5, 22, 14), updated: day(5, 22, 15),
			usage: usage{Input: 500, Output: 250}},
		{id: "s3", model: "claude-sonnet-4-6", started: day(5, 21, 9), updated: day(5, 21, 10),
			usage: usage{Input: 800, Output: 400}},
		{id: "s4", model: "claude-opus-4-7", started: day(4, 20, 9), updated: day(4, 20, 10),
			usage: usage{Input: 9_999, Output: 9_999}},
	})

	buckets, err := TokensByDay(store, now.AddDate(0, 0, -30), now, "")
	if err != nil {
		t.Fatal(err)
	}

	// 5/22 combines both opus sessions (1500 in, 750 out, 2000 cache-r).
	// 5/21 has the sonnet only. 4/20 must be excluded by the window.
	got := bucketsByDate(buckets)
	if g := got["2026-05-22"]; g.Tokens.Input != 1500 || g.SessionCount != 2 {
		t.Errorf("5/22 bucket: got %+v, want Input=1500 SessionCount=2", g)
	}
	if g := got["2026-05-21"]; g.Tokens.Input != 800 || g.SessionCount != 1 {
		t.Errorf("5/21 bucket: got %+v", g)
	}
	if _, ok := got["2026-04-20"]; ok {
		t.Errorf("4/20 should be outside the window")
	}
}

func TestTokensByDay_longRunningSessionIncludedByUpdatedAt(t *testing.T) {
	// A session that started 35 days ago but was last active 2 days ago
	// must appear in a 30-day window — the filter is UpdatedAt, not
	// StartedAt, so we don't lose sessions that span the boundary.
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	startedLongAgo := now.AddDate(0, 0, -35)
	updatedRecently := now.AddDate(0, 0, -2)
	store := newTestStore(t, []sessionSpec{
		{id: "long", model: "claude-opus-4-7", started: startedLongAgo, updated: updatedRecently,
			usage: usage{Input: 1234}},
	})

	got, err := TokensByDay(store, now.AddDate(0, 0, -30), now, "")
	if err != nil {
		t.Fatal(err)
	}
	key := updatedRecently.UTC().Format("2006-01-02")
	if bucketsByDate(got)[key].Tokens.Input != 1234 {
		t.Errorf("long-running session should appear under its UpdatedAt day %q, got buckets %+v", key, got)
	}
}

func TestTokensByDay_filtersByModel(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, []sessionSpec{
		{id: "s1", model: "claude-opus-4-7", started: day(5, 22, 10), updated: day(5, 22, 11), usage: usage{Input: 100}},
		{id: "s2", model: "claude-sonnet-4-6", started: day(5, 22, 10), updated: day(5, 22, 11), usage: usage{Input: 200}},
	})

	got, err := TokensByDay(store, now.AddDate(0, 0, -30), now, "opus")
	if err != nil {
		t.Fatal(err)
	}
	if total := bucketsByDate(got)["2026-05-22"].Tokens.Input; total != 100 {
		t.Errorf("opus-only filter: got %d, want 100", total)
	}
}
