package analytics

import (
	"time"

	"github.com/jonasross/canopy/sessions"
)

// Build assembles the four sub-views in one call. Window is fixed at
// 30 days back from now; tune in a later iteration if needed.
// RecentSessions is capped at 20 — enough rows to fill a terminal page;
// cheaper than hydrating everything.
const (
	snapshotWindow      = 30 * 24 * time.Hour
	recentSessionsLimit = 20
)

// Build computes a full Snapshot against store at the given instant.
func Build(store *sessions.Store, now time.Time) (Snapshot, error) {
	since := now.Add(-snapshotWindow)

	days, err := TokensByDay(store, since, now, "")
	if err != nil {
		return Snapshot{}, err
	}
	recent, err := RecentSessions(store, recentSessionsLimit)
	if err != nil {
		return Snapshot{}, err
	}
	tools, err := ToolDistribution(store, since, now)
	if err != nil {
		return Snapshot{}, err
	}
	wts, err := PerWorktree(store, "", since)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		GeneratedAt: now,
		WindowStart: since,
		WindowEnd:   now,
		Days:        days,
		Sessions:    recent,
		Tools:       tools,
		Worktrees:   wts,
	}, nil
}
