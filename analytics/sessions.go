package analytics

import (
	"sort"
	"time"

	"github.com/jonasross/canopy/sessions"
)

// RecentSessions returns the n most-recently-updated sessions, sorted
// DESC by UpdatedAt. If the store has fewer than n sessions, all are
// returned. Each session is Hydrated before being included.
func RecentSessions(store *sessions.Store, n int) ([]SessionSummary, error) {
	// Defensive: callers can pass arbitrary n. Negative would slice with
	// a negative index further down and panic; zero is a no-op.
	if n <= 0 {
		return nil, nil
	}
	var all []*sessions.Session
	for sess := range store.Sessions() {
		all = append(all, sess)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].UpdatedAt.After(all[j].UpdatedAt) })
	if n > len(all) {
		n = len(all)
	}
	out := make([]SessionSummary, 0, n)
	for _, sess := range all[:n] {
		if err := store.Hydrate(sess); err != nil {
			return nil, err
		}
		prompts, active, err := sessionStats(store, sess.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, summarize(sess, prompts, active))
	}
	return out, nil
}

func summarize(sess *sessions.Session, prompts int, active time.Duration) SessionSummary {
	worktree := ""
	if len(sess.Cwds) > 0 {
		worktree = sess.Cwds[len(sess.Cwds)-1] // deepest / most-recent
	}
	tools := 0
	for _, c := range sess.Tools {
		tools += c
	}
	return SessionSummary{
		ID:          sess.ID,
		Model:       sess.Model,
		Worktree:    worktree,
		StartedAt:   sess.StartedAt,
		UpdatedAt:   sess.UpdatedAt,
		Duration:    active,
		Prompts:     prompts,
		ToolCalls:   tools,
		Tokens:      sess.Tokens,
		IsSidechain: sess.IsSidechain,
	}
}

// maxIdleGap is the threshold beyond which a between-events gap is
// treated as inactivity (the user walked away) rather than engaged
// time. Caps each individual gap; total active time is the sum of
// gaps each capped at this ceiling.
//
// 10 min covers normal thinking pauses and short reading breaks
// without inflating totals for sessions left open overnight. Tune
// here if real usage diverges.
const maxIdleGap = 10 * time.Minute

// sessionStats walks a session's events once and returns the user
// prompt count and the gap-capped active duration. Active duration
// sums per-event-gap durations, capping each at maxIdleGap; this
// replaces the older wallclock UpdatedAt-StartedAt formula, which
// over-counted abandoned sessions (one prompt, opened and forgotten,
// could show as many hours).
//
// Tool-result lines are surfaced by the sessions package as
// EventToolResult (not EventUser), so every EventUser is a real
// user prompt.
func sessionStats(store *sessions.Store, sessionID string) (prompts int, active time.Duration, err error) {
	var prev time.Time
	for ev, e := range store.Events(sessionID) {
		if e != nil {
			return 0, 0, e
		}
		if ev.Kind == sessions.EventUser {
			prompts++
		}
		if ev.Timestamp.IsZero() {
			continue
		}
		if !prev.IsZero() {
			if gap := ev.Timestamp.Sub(prev); gap > 0 {
				if gap > maxIdleGap {
					gap = maxIdleGap
				}
				active += gap
			}
		}
		prev = ev.Timestamp
	}
	return prompts, active, nil
}
