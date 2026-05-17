package pr

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withFakeClock installs a deterministic clock backed by the returned
// pointer. Tests advance time by mutating *clk.
func withFakeClock(t *testing.T) *time.Time {
	t.Helper()
	clk := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	orig := now
	now = func() time.Time { return clk }
	t.Cleanup(func() { now = orig })
	return &clk
}

// withRunStub installs a runCmd stub backed by an atomic counter and
// the given response producer. The returned counter records the
// number of runCmd invocations (= one per List call) and lets tests
// assert call-count semantics.
func withRunStub(t *testing.T, fn func() ([]byte, error)) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	stubLookPath(t, nil)
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		calls.Add(1)
		return fn()
	})
	return &calls
}

func TestCache_ReturnsFreshOnFirstCall(t *testing.T) {
	withFakeClock(t)
	calls := withRunStub(t, func() ([]byte, error) {
		return []byte("[]"), nil
	})

	c := NewCache(30 * time.Second)
	prs, stale, err := c.Get(context.Background(), "/tmp/repo")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if stale {
		t.Fatalf("Get: stale=true on first call, want false")
	}
	if prs == nil {
		t.Fatalf("Get: prs=nil, want non-nil empty slice")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("List call count=%d, want 1", got)
	}
}

func TestCache_CachesWithinTTL(t *testing.T) {
	clk := withFakeClock(t)
	calls := withRunStub(t, func() ([]byte, error) {
		return []byte("[]"), nil
	})

	c := NewCache(30 * time.Second)
	if _, _, err := c.Get(context.Background(), "/tmp/repo"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Advance 10s — still inside the 30s TTL.
	*clk = clk.Add(10 * time.Second)
	if _, _, err := c.Get(context.Background(), "/tmp/repo"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("List call count=%d, want 1 (still cached)", got)
	}
}

func TestCache_RefreshesAfterTTL(t *testing.T) {
	clk := withFakeClock(t)
	calls := withRunStub(t, func() ([]byte, error) {
		return []byte("[]"), nil
	})

	c := NewCache(30 * time.Second)
	if _, _, err := c.Get(context.Background(), "/tmp/repo"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Step past the TTL boundary.
	*clk = clk.Add(31 * time.Second)
	if _, _, err := c.Get(context.Background(), "/tmp/repo"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("List call count=%d, want 2 (TTL elapsed)", got)
	}
}

func TestCache_StaleOnError(t *testing.T) {
	clk := withFakeClock(t)

	var failNext atomic.Bool
	calls := withRunStub(t, func() ([]byte, error) {
		if failNext.Load() {
			return nil, errors.New("gh: HTTP 500")
		}
		return []byte(`[{"number":1,"title":"first","headRefName":"feat/x","state":"OPEN","isDraft":false,"statusCheckRollup":[],"reviewDecision":"","mergedAt":"","updatedAt":"2026-05-17T12:00:00Z","url":"https://example/1"}]`), nil
	})

	c := NewCache(30 * time.Second)
	first, stale, err := c.Get(context.Background(), "/tmp/repo")
	if err != nil || stale {
		t.Fatalf("first Get err=%v stale=%v, want clean", err, stale)
	}
	if len(first) != 1 {
		t.Fatalf("first len=%d, want 1", len(first))
	}

	// Cause the next refresh to fail and step past TTL.
	failNext.Store(true)
	*clk = clk.Add(31 * time.Second)

	second, stale, err := c.Get(context.Background(), "/tmp/repo")
	if err != nil {
		t.Fatalf("second Get err=%v, want nil (stale-on-error)", err)
	}
	if !stale {
		t.Fatalf("second Get stale=false, want true")
	}
	if len(second) != 1 || second[0].Number != 1 {
		t.Fatalf("second Get returned %+v, want previous cached result", second)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("List call count=%d, want 2", got)
	}
}

func TestCache_NoPreviousAndErrorPropagates(t *testing.T) {
	withFakeClock(t)
	withRunStub(t, func() ([]byte, error) {
		return nil, errors.New("gh: HTTP 500")
	})

	c := NewCache(30 * time.Second)
	prs, stale, err := c.Get(context.Background(), "/tmp/repo")
	if err == nil {
		t.Fatalf("Get err=nil, want non-nil (no previous result to fall back to)")
	}
	if stale {
		t.Fatalf("Get stale=true on first-ever failure, want false")
	}
	if prs != nil {
		t.Fatalf("Get prs=%v, want nil", prs)
	}
}

func TestCache_Invalidate(t *testing.T) {
	withFakeClock(t)
	calls := withRunStub(t, func() ([]byte, error) {
		return []byte("[]"), nil
	})

	c := NewCache(30 * time.Second)
	if _, _, err := c.Get(context.Background(), "/tmp/repo"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	c.Invalidate("/tmp/repo")
	// Still well inside the TTL — but Invalidate must force a refresh.
	if _, _, err := c.Get(context.Background(), "/tmp/repo"); err != nil {
		t.Fatalf("post-Invalidate Get: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("List call count=%d, want 2 (Invalidate forces refresh)", got)
	}

	// Invalidate on an unknown key is a no-op.
	c.Invalidate("/tmp/unknown")
}

func TestCache_ConcurrentGetsCoalesce(t *testing.T) {
	withFakeClock(t)

	// Block the first List inside the stub until release is closed.
	// Subsequent calls return immediately, but if coalescing works
	// there should not be any.
	release := make(chan struct{})
	var calls atomic.Int64
	stubLookPath(t, nil)
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		calls.Add(1)
		<-release
		return []byte("[]"), nil
	})

	c := NewCache(30 * time.Second)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	started := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			started <- struct{}{}
			if _, _, err := c.Get(context.Background(), "/tmp/repo"); err != nil {
				t.Errorf("concurrent Get: %v", err)
			}
		}()
	}
	// Wait until all goroutines have at least entered the goroutine
	// body so we know they all raced into Get. The fetchMu
	// serialization inside Cache.Get is what we're testing; some
	// goroutines will be inside Get waiting on fetchMu, the rest
	// inside the runCmd stub waiting on release.
	for i := 0; i < n; i++ {
		<-started
	}
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("List call count=%d, want 1 (coalesced)", got)
	}
}

func TestCache_RaceClean(t *testing.T) {
	withFakeClock(t)
	withRunStub(t, func() ([]byte, error) {
		return []byte("[]"), nil
	})

	c := NewCache(30 * time.Second)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n * 3)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _, _ = c.Get(context.Background(), "/tmp/repo")
		}()
		go func() {
			defer wg.Done()
			c.Invalidate("/tmp/repo")
		}()
		go func() {
			defer wg.Done()
			_, _, _ = c.Get(context.Background(), "/tmp/repo")
		}()
	}
	wg.Wait()
}
