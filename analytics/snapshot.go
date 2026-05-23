package analytics

import (
	"time"

	"github.com/jonasross/canopy/sessions"
)

const (
	snapshotWindow      = 30 * 24 * time.Hour
	recentSessionsLimit = 20
)

// Build computes a full Snapshot against store at the given instant —
// per-day spend, recent sessions, tool distribution, and per-worktree
// totals, all bounded to the 30-day window ending at now.
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

	sessionsByModel := map[string]int{}
	for sess := range store.Sessions() {
		if sess.UpdatedAt.Before(since) || sess.UpdatedAt.After(now) {
			continue
		}
		if sess.Model == "" {
			continue
		}
		sessionsByModel[sess.Model]++
	}

	return Snapshot{
		GeneratedAt:         now,
		WindowStart:         since,
		WindowEnd:           now,
		Days:                days,
		Sessions:            recent,
		Tools:               tools,
		Worktrees:           wts,
		SessionCountByModel: sessionsByModel,
	}, nil
}
