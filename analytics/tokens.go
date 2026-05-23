package analytics

import (
	"sort"
	"strings"
	"time"

	"github.com/jonasross/canopy/sessions"
)

// TokensByDay returns one bucket per UTC day in [since, until] that had
// any activity, sorted DESC by Date. modelFilter is a case-insensitive
// substring; "" matches any model (the spec calls these the "all models"
// rollup). A session whose UpdatedAt falls within the window contributes
// its full Tokens to its UpdatedAt day — we treat the session as a
// single point at its most-recent-activity day because usage objects
// are already deduplicated at Hydrate time, and UpdatedAt is what users
// mean by "when did this session happen". Long-running sessions that
// started before the window but were active within it are included.
func TokensByDay(store *sessions.Store, since, until time.Time, modelFilter string) ([]DayBucket, error) {
	type key struct{ day time.Time }
	agg := map[key]*DayBucket{}

	for sess := range store.Sessions() {
		if !since.IsZero() && sess.UpdatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && sess.UpdatedAt.After(until) {
			continue
		}
		if err := store.Hydrate(sess); err != nil {
			return nil, err
		}
		if modelFilter != "" && !strings.Contains(strings.ToLower(sess.Model), strings.ToLower(modelFilter)) {
			continue
		}
		dayUTC := time.Date(sess.UpdatedAt.Year(), sess.UpdatedAt.Month(), sess.UpdatedAt.Day(), 0, 0, 0, 0, time.UTC)
		k := key{day: dayUTC}
		b, ok := agg[k]
		if !ok {
			b = &DayBucket{Date: dayUTC}
			agg[k] = b
		}
		b.Tokens.Input += sess.Tokens.Input
		b.Tokens.Output += sess.Tokens.Output
		b.Tokens.CacheRead += sess.Tokens.CacheRead
		b.Tokens.CacheCreation += sess.Tokens.CacheCreation
		b.SessionCount++
		// Tag bucket with model when filter is narrow; otherwise leave "".
		if modelFilter != "" && b.Model == "" {
			b.Model = strings.ToLower(modelFilter)
		}
	}

	out := make([]DayBucket, 0, len(agg))
	for _, b := range agg {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out, nil
}
