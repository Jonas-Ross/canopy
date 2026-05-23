package analytics

import (
	"time"

	"github.com/jonasross/canopy/sessions"
)

const (
	snapshotWindowDays  = 30
	recentSessionsLimit = 20
)

// Build computes a full Snapshot against store at the given instant —
// per-day spend, recent sessions, tool distribution, and per-worktree
// totals, all bounded to the 30-day window ending at now. WindowStart is
// the UTC midnight 29 days before now's UTC day, so an inclusive render
// of [WindowStart, now] truncated to days yields exactly 30 calendar rows.
func Build(store *sessions.Store, now time.Time) (Snapshot, error) {
	nowUTC := now.UTC()
	endDay := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	startDay := endDay.AddDate(0, 0, -(snapshotWindowDays - 1))

	days, err := TokensByDay(store, startDay, now, "")
	if err != nil {
		return Snapshot{}, err
	}
	recent, err := RecentSessions(store, recentSessionsLimit)
	if err != nil {
		return Snapshot{}, err
	}
	tools, err := ToolDistribution(store, startDay, now)
	if err != nil {
		return Snapshot{}, err
	}
	wts, err := PerWorktree(store, "", startDay)
	if err != nil {
		return Snapshot{}, err
	}

	sessionsByModel := map[string]int{}
	for sess := range store.Sessions() {
		if sess.UpdatedAt.Before(startDay) || sess.UpdatedAt.After(now) {
			continue
		}
		if sess.Model == "" {
			continue
		}
		sessionsByModel[sess.Model]++
	}

	return Snapshot{
		GeneratedAt:         now,
		WindowStart:         startDay,
		WindowEnd:           now,
		Days:                days,
		Sessions:            recent,
		Tools:               tools,
		Worktrees:           wts,
		SessionCountByModel: sessionsByModel,
	}, nil
}
