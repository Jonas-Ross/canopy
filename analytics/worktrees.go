package analytics

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jonasross/canopy/sessions"
)

// PerWorktree groups sessions by their deepest cwd (the last entry in
// sess.Cwds after Hydrate). repoRoot, when non-empty, restricts results
// to sessions whose cwd is equal to or nested under repoRoot. Pass "" to
// include all sessions in the store. since filters by UpdatedAt.
func PerWorktree(store *sessions.Store, repoRoot string, since time.Time) ([]WorktreeSummary, error) {
	agg := map[string]*WorktreeSummary{}

	for sess := range store.Query(sessions.Query{Since: since}) {
		if err := store.Hydrate(sess); err != nil {
			return nil, err
		}
		if len(sess.Cwds) == 0 {
			continue
		}
		path := sess.Cwds[len(sess.Cwds)-1]
		if repoRoot != "" && !pathHasPrefix(path, repoRoot) {
			continue
		}
		w, ok := agg[path]
		if !ok {
			w = &WorktreeSummary{Path: path}
			agg[path] = w
		}
		w.SessionCount++
		w.TotalTime += sess.UpdatedAt.Sub(sess.StartedAt)
		if sess.UpdatedAt.After(w.LastSeen) {
			w.LastSeen = sess.UpdatedAt
		}
	}

	out := make([]WorktreeSummary, 0, len(agg))
	for _, w := range agg {
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalTime > out[j].TotalTime })
	return out, nil
}

// pathHasPrefix mirrors the aggregator's CwdPrefix logic: prefix match
// with a trailing separator semantic so "/a/b" doesn't match "/a/bb".
func pathHasPrefix(p, prefix string) bool {
	if p == prefix {
		return true
	}
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	rest := p[len(prefix):]
	return len(rest) > 0 && rest[0] == filepath.Separator
}
