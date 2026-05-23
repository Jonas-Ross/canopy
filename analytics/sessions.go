package analytics

import (
	"sort"

	"github.com/jonasross/canopy/sessions"
)

// RecentSessions returns the n most-recently-updated sessions, sorted
// DESC by UpdatedAt. If the store has fewer than n sessions, all are
// returned. Each session is Hydrated before being included.
func RecentSessions(store *sessions.Store, n int) ([]SessionSummary, error) {
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
		pc, err := promptCount(store, sess)
		if err != nil {
			return nil, err
		}
		out = append(out, summarize(sess, pc))
	}
	return out, nil
}

func summarize(sess *sessions.Session, prompts int) SessionSummary {
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
		Duration:    sess.UpdatedAt.Sub(sess.StartedAt),
		Prompts:     prompts,
		ToolCalls:   tools,
		Tokens:      sess.Tokens,
		IsSidechain: sess.IsSidechain,
	}
}

// promptCount counts the number of genuine user prompts in the session.
// Tool-result lines are surfaced by the sessions package as EventToolResult
// (not EventUser), so every EventUser event is a real user prompt.
// Any iteration error is returned to the caller.
func promptCount(store *sessions.Store, sess *sessions.Session) (int, error) {
	count := 0
	for ev, err := range store.Events(sess.ID) {
		if err != nil {
			return 0, err
		}
		if ev.Kind == sessions.EventUser {
			count++
		}
	}
	return count, nil
}
