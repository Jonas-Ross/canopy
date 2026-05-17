package pr

import (
	"context"
	"sync"
	"time"
)

// now is the package-level clock. Tests override it to advance time
// deterministically without sleeping; production code reads the real
// wall clock. This is the only time source used by Cache.
var now = time.Now

// cacheEntry is the per-repo cell behind Cache. fetchMu serializes
// concurrent refreshes for the same key so a burst of callers
// produces exactly one underlying List call; the result is published
// under the cell mutex so subsequent reads see the fresh value.
type cacheEntry struct {
	// mu guards prs, fetchedAt, ok, and the validity of the cell as a
	// whole. fetchMu (below) is held only across the runCmd
	// invocation, and is acquired before mu to avoid lock-order
	// hazards with the Get hot path.
	mu        sync.Mutex
	prs       []PR
	fetchedAt time.Time
	// ok is true once we've ever successfully populated prs. It is
	// the gate for stale-on-error: a failed refresh returns the
	// previous result only when ok==true.
	ok bool

	// fetchMu coalesces concurrent refreshes for this key.
	// Callers acquire it before doing the (expensive) List call; the
	// second caller waits and then re-checks freshness under mu.
	fetchMu sync.Mutex
}

// Cache is a TTL-bounded, stale-on-error wrapper over List. It is
// safe for concurrent use; concurrent Gets for the same repoRoot
// coalesce to a single underlying List invocation.
//
// Semantics:
//
//   - First Get on a key triggers a synchronous List. The result is
//     stored and returned with stale=false.
//   - Subsequent Gets within TTL return the cached payload immediately
//     with stale=false. No List invocation.
//   - Gets after TTL refresh synchronously. On success, the cell is
//     updated and stale=false. On failure with a previous successful
//     result, the cached payload is returned with stale=true and a
//     nil error (the "stale-on-error" path documented in
//     docs/handoff.md §"PR integration — Failure modes").
//   - Gets after TTL with no previous successful result propagate the
//     error.
//   - Invalidate forces the next Get to refresh regardless of TTL.
type Cache struct {
	ttl time.Duration

	// entries is keyed by repoRoot. The mutex guards the map
	// structure only; per-entry state has its own mu/fetchMu.
	entriesMu sync.Mutex
	entries   map[string]*cacheEntry
}

// NewCache constructs a Cache with the given TTL. TTL <= 0 is
// allowed and means "always refresh" (entries are immediately stale);
// production code should pass 30s per the handoff.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl:     ttl,
		entries: make(map[string]*cacheEntry),
	}
}

// getOrCreate returns the *cacheEntry for repoRoot, creating it if
// needed. Holds entriesMu only long enough to look up or insert.
func (c *Cache) getOrCreate(repoRoot string) *cacheEntry {
	c.entriesMu.Lock()
	defer c.entriesMu.Unlock()
	e, ok := c.entries[repoRoot]
	if !ok {
		e = &cacheEntry{}
		c.entries[repoRoot] = e
	}
	return e
}

// Get returns the cached PRs for repoRoot, refreshing if the TTL has
// expired or the cell has been invalidated. The stale return is true
// when the underlying refresh failed and we are serving a previously
// cached result; in that case err is nil.
//
// Concurrent Gets for the same repoRoot coalesce: only one List call
// is in flight per key at a time. Callers that arrive while a
// refresh is in progress block on fetchMu, then re-check freshness
// under mu — the typical effect is that they see the fresh result
// without triggering a second refresh.
func (c *Cache) Get(ctx context.Context, repoRoot string) ([]PR, bool, error) {
	e := c.getOrCreate(repoRoot)

	e.mu.Lock()
	if e.ok && c.ttl > 0 && now().Sub(e.fetchedAt) < c.ttl {
		out := e.prs
		e.mu.Unlock()
		return out, false, nil
	}
	e.mu.Unlock()

	// Coalesce concurrent refreshes for this key: serialize on
	// fetchMu, then re-check freshness so a peer's just-completed
	// refresh isn't redone.
	e.fetchMu.Lock()
	defer e.fetchMu.Unlock()

	e.mu.Lock()
	if e.ok && c.ttl > 0 && now().Sub(e.fetchedAt) < c.ttl {
		out := e.prs
		e.mu.Unlock()
		return out, false, nil
	}
	e.mu.Unlock()

	prs, err := List(ctx, repoRoot)
	if err != nil {
		// Stale-on-error: hand back the previous result if we have
		// one, swallowing the error so the UI can stay live. Without
		// a previous result the error must propagate so callers can
		// distinguish "never worked" from "transient blip."
		e.mu.Lock()
		if e.ok {
			out := e.prs
			e.mu.Unlock()
			return out, true, nil
		}
		e.mu.Unlock()
		return nil, false, err
	}

	e.mu.Lock()
	e.prs = prs
	e.fetchedAt = now()
	e.ok = true
	e.mu.Unlock()
	return prs, false, nil
}

// Invalidate forces the next Get for repoRoot to refresh regardless
// of TTL. Existing cached data (if any) is preserved so a refresh
// failure can still fall back via stale-on-error; only the freshness
// clock is reset.
//
// Safe to call from any goroutine; safe to call for a key that has
// never been Get'd (no-op in that case).
func (c *Cache) Invalidate(repoRoot string) {
	c.entriesMu.Lock()
	e, ok := c.entries[repoRoot]
	c.entriesMu.Unlock()
	if !ok {
		return
	}
	e.mu.Lock()
	e.fetchedAt = time.Time{}
	e.mu.Unlock()
}
