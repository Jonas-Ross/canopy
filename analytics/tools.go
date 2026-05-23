package analytics

import (
	"sort"
	"time"

	"github.com/jonasross/canopy/sessions"
)

// ToolDistribution returns tool usage bucketed by (Model, Tool), sorted
// by model ascending then count descending within each model. Only
// sessions whose UpdatedAt falls in [since, until] are considered.
func ToolDistribution(store *sessions.Store, since, until time.Time) ([]ToolUsage, error) {
	type key struct{ model, tool string }
	agg := map[key]int{}

	for sess := range store.Query(sessions.Query{Since: since, Until: until}) {
		if err := store.Hydrate(sess); err != nil {
			return nil, err
		}
		if sess.Model == "" {
			continue
		}
		for tool, count := range sess.Tools {
			agg[key{sess.Model, tool}] += count
		}
	}

	out := make([]ToolUsage, 0, len(agg))
	for k, c := range agg {
		out = append(out, ToolUsage{Model: k.model, Tool: k.tool, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Count > out[j].Count
	})
	return out, nil
}
