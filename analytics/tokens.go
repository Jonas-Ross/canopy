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
// rollup). A session whose StartedAt falls within the window contributes
// its full Tokens to its StartedAt day — we treat the session as a single
// point rather than spreading per-event because usage objects are already
// deduplicated at Hydrate time.
func TokensByDay(store *sessions.Store, since, until time.Time, modelFilter string) ([]DayBucket, error) {
	type key struct{ day time.Time }
	agg := map[key]*DayBucket{}

	q := sessions.Query{Since: since, Until: until}
	if modelFilter != "" {
		q.Model = modelFilter
	}
	for sess := range store.Query(q) {
		if err := store.Hydrate(sess); err != nil {
			return nil, err
		}
		// Additional substring filter if specified (Query.Model is already
		// a substring match, but apply defensively here for clarity).
		if modelFilter != "" && !strings.Contains(strings.ToLower(sess.Model), strings.ToLower(modelFilter)) {
			continue
		}
		dayUTC := time.Date(sess.StartedAt.Year(), sess.StartedAt.Month(), sess.StartedAt.Day(), 0, 0, 0, 0, time.UTC)
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
