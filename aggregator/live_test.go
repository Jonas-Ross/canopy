package aggregator

import (
	"context"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonasross/canopy/git"
)

// liveFixture builds a minimal aggregator with a tempdir-rooted
// session store and a single repo. The fakeSources is exposed so the
// test can mutate it before/after Start to drive change detection.
type liveFixture struct {
	a    *Aggregator
	f    *fakeSources
	repo string
	wt1  string
	wt2  string
}

func newLiveFixture(t *testing.T) *liveFixture {
	t.Helper()
	repo := "/repo"
	wt1 := "/repo/wt-a"
	wt2 := "/repo/wt-b"

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repo: {
				{Path: wt1, Branch: "feat/a"},
				{Path: wt2, Branch: "feat/b"},
			},
		},
		statuses: map[string]git.Worktree{
			wt1: {Path: wt1, Branch: "feat/a", DirtyFiles: 0},
			wt2: {Path: wt2, Branch: "feat/b", DirtyFiles: 0},
		},
	}

	now := fixedNow()
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repo, Name: "repo"}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcs:      fakes.listProcs,
		now:            func() time.Time { return now },
	})
	return &liveFixture{a: a, f: fakes, repo: repo, wt1: wt1, wt2: wt2}
}

// drain reads up to max updates from ch or returns whatever has
// arrived within d. Channel closure terminates early.
func drain(t *testing.T, ch <-chan Update, max int, d time.Duration) []Update {
	t.Helper()
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	var out []Update
	for len(out) < max {
		select {
		case u, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, u)
		case <-deadline.C:
			return out
		}
	}
	return out
}

func TestStart_EmitsBaselineUpdatePerWorktree(t *testing.T) {
	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fx.a.Close()

	sub := fx.a.Subscribe(context.Background())

	got := drain(t, sub, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("baseline updates: got %d, want 2 (paths=%v)", len(got), updatePaths(got))
	}
	paths := updatePaths(got)
	if !containsString(paths, fx.wt1) || !containsString(paths, fx.wt2) {
		t.Errorf("baseline paths=%v; want both %s and %s", paths, fx.wt1, fx.wt2)
	}
}

func TestRefresh_EmitsUpdateWhenStateChanges(t *testing.T) {
	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fx.a.Close()

	sub := fx.a.Subscribe(context.Background())
	// Drain baseline.
	_ = drain(t, sub, 2, time.Second)

	// Mutate the fake: wt-a now reports 5 dirty files.
	fx.f.mu.Lock()
	fx.f.statuses[fx.wt1] = git.Worktree{Path: fx.wt1, Branch: "feat/a", DirtyFiles: 5}
	fx.f.mu.Unlock()

	fx.a.Refresh()

	got := drain(t, sub, 1, time.Second)
	if len(got) != 1 {
		t.Fatalf("after Refresh: got %d updates, want 1", len(got))
	}
	if got[0].Worktree != fx.wt1 {
		t.Errorf("update path=%q, want %q", got[0].Worktree, fx.wt1)
	}
	if got[0].State.Worktree.DirtyFiles != 5 {
		t.Errorf("DirtyFiles=%d, want 5", got[0].State.Worktree.DirtyFiles)
	}
}

// Regression: refreshOne (cmdRefreshByCwd / cmdRefreshWorktree) is the
// fsnotify- and tail-driven path. It calls WorktreeStatus directly, which
// does not populate the identity flags set by ListWorktrees (Main, Bare).
// Without restoring them, the next Update drops Main on the primary
// worktree, which re-arms the TUI's prune prompt on `d` — exactly the
// PR #28 bug, just via a different code path.
func TestRefreshByCwd_PreservesMainOnPrimary(t *testing.T) {
	repo := "/repo"
	primary := "/repo"
	secondary := "/repo/wt-a"

	store := openTestSessionStore(t, func(root string) {})
	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repo: {
				{Path: primary, Branch: "main", Main: true},
				{Path: secondary, Branch: "feat/a"},
			},
		},
		statuses: map[string]git.Worktree{
			primary:   {Path: primary, Branch: "main", DirtyFiles: 0, HasUpstream: true},
			secondary: {Path: secondary, Branch: "feat/a", DirtyFiles: 0, HasUpstream: true},
		},
	}
	now := fixedNow()
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repo, Name: "repo"}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcs:      fakes.listProcs,
		now:            func() time.Time { return now },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Close()

	sub := a.Subscribe(context.Background())
	baseline := drain(t, sub, 2, time.Second)
	if len(baseline) != 2 {
		t.Fatalf("baseline updates: got %d, want 2", len(baseline))
	}
	for _, u := range baseline {
		if u.Worktree == primary && !u.State.Worktree.Main {
			t.Fatalf("baseline primary Main=false; refreshAll path itself is broken")
		}
	}

	// Mutate so the refreshOne path emits a new update (otherwise the
	// state-equality short-circuit swallows it and we can't observe the
	// new Main value).
	fakes.mu.Lock()
	fakes.statuses[primary] = git.Worktree{Path: primary, Branch: "main", DirtyFiles: 3, HasUpstream: true}
	fakes.mu.Unlock()

	// cmdRefreshByCwd is what fsnotify and the tail consumer send; route
	// through the same channel the production callers use.
	a.cmds <- command{kind: cmdRefreshByCwd, cwd: primary}

	got := drain(t, sub, 1, time.Second)
	if len(got) != 1 {
		t.Fatalf("after cmdRefreshByCwd: got %d updates, want 1", len(got))
	}
	if got[0].Worktree != primary {
		t.Fatalf("update path=%q, want %q", got[0].Worktree, primary)
	}
	if got[0].State.Worktree.DirtyFiles != 3 {
		t.Errorf("DirtyFiles=%d, want 3 (mutation should propagate)", got[0].State.Worktree.DirtyFiles)
	}
	if !got[0].State.Worktree.Main {
		t.Errorf("primary Worktree.Main = false after refreshOne; want true — identity flags must survive the WorktreeStatus merge on the live-refresh path, not just Snapshot")
	}
}

func TestRefresh_NoEmitWhenStateUnchanged(t *testing.T) {
	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fx.a.Close()

	sub := fx.a.Subscribe(context.Background())
	_ = drain(t, sub, 2, time.Second)

	// No source mutation. Refresh must not produce any updates.
	fx.a.Refresh()

	got := drain(t, sub, 1, 250*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("no-change Refresh produced %d updates (first=%+v)", len(got), got[0])
	}
}

func TestSubscribe_MultipleSubscribers_AllReceive(t *testing.T) {
	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fx.a.Close()

	s1 := fx.a.Subscribe(context.Background())
	s2 := fx.a.Subscribe(context.Background())

	// Drain baselines for both.
	_ = drain(t, s1, 2, time.Second)
	_ = drain(t, s2, 2, time.Second)

	fx.f.mu.Lock()
	fx.f.statuses[fx.wt1] = git.Worktree{Path: fx.wt1, Branch: "feat/a", DirtyFiles: 9}
	fx.f.mu.Unlock()
	fx.a.Refresh()

	got1 := drain(t, s1, 1, time.Second)
	got2 := drain(t, s2, 1, time.Second)
	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("subscriber counts: s1=%d s2=%d", len(got1), len(got2))
	}
}

func TestSubscribe_SlowSubscriberDrops(t *testing.T) {
	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fx.a.Close()

	slow := fx.a.Subscribe(context.Background()) // never read
	fast := fx.a.Subscribe(context.Background())

	// Fast reader: drain baselines.
	_ = drain(t, fast, 2, time.Second)
	_ = slow // do not read

	// Generate enough state changes that slow's buffer (64) fills.
	// Each Refresh re-evaluates all worktrees and emits ~2 per
	// distinct state. We toggle DirtyFiles to alternate.
	for i := 0; i < 200; i++ {
		fx.f.mu.Lock()
		fx.f.statuses[fx.wt1] = git.Worktree{Path: fx.wt1, Branch: "feat/a", DirtyFiles: i + 1}
		fx.f.mu.Unlock()
		fx.a.Refresh()
		// Let the fast reader keep up.
		drained := drain(t, fast, 1, 50*time.Millisecond)
		if drained == nil {
			// Don't care; just throttle.
		}
	}

	// Wait for any in-flight refreshes to land in the loop.
	time.Sleep(100 * time.Millisecond)

	stats := fx.a.Stats()
	if stats.SubscriberDrops == 0 {
		t.Errorf("SubscriberDrops=0; expected nonzero from slow subscriber")
	}
}

func TestSubscribe_ChannelClosesOnCtxCancel(t *testing.T) {
	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fx.a.Close()

	subCtx, subCancel := context.WithCancel(context.Background())
	doomed := fx.a.Subscribe(subCtx)
	other := fx.a.Subscribe(context.Background())

	// Drain baselines.
	_ = drain(t, doomed, 2, time.Second)
	_ = drain(t, other, 2, time.Second)

	subCancel()

	// Expect doomed to close.
	closed := false
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !closed {
		select {
		case _, ok := <-doomed:
			if !ok {
				closed = true
			}
		case <-deadline.C:
			t.Fatalf("doomed channel did not close after ctx cancel")
		}
	}

	// Other subscriber is still alive: triggering a refresh should
	// still deliver to it.
	fx.f.mu.Lock()
	fx.f.statuses[fx.wt1] = git.Worktree{Path: fx.wt1, Branch: "feat/a", DirtyFiles: 11}
	fx.f.mu.Unlock()
	fx.a.Refresh()

	got := drain(t, other, 1, time.Second)
	if len(got) != 1 {
		t.Errorf("other subscriber lost updates: got %d", len(got))
	}
}

func TestClose_StopsAllGoroutines(t *testing.T) {
	// Allow goroutines from previous tests (or runtime) to settle so
	// the baseline we capture is stable. Skip on the race detector
	// where pprof may keep some live; the assertion uses tolerance.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Subscribe spins up an extra goroutine to watch ctx; create one
	// so Close has to clean it up.
	_ = fx.a.Subscribe(context.Background())

	// Give Start time to spin up all goroutines.
	time.Sleep(50 * time.Millisecond)
	if got := runtime.NumGoroutine(); got <= baseline {
		t.Logf("Start did not visibly spawn goroutines (got=%d baseline=%d); test may be a no-op", got, baseline)
	}

	fx.a.Close()

	// After Close + a brief settle, goroutines should return to near
	// the baseline. Allow +2 slack for runtime scheduling artifacts.
	deadline := time.Now().Add(2 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		got = runtime.NumGoroutine()
		if got <= baseline+2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("goroutines after Close=%d, baseline=%d (+2 tolerance)", got, baseline)
}

func TestStart_TwiceErrors(t *testing.T) {
	fx := newLiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fx.a.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer fx.a.Close()
	if err := fx.a.Start(ctx); err == nil {
		t.Errorf("second Start: got nil, want error")
	}
}

func TestTailEventTriggersRefresh(t *testing.T) {
	// This test exercises the wiring sessions.Tail -> tailConsumer ->
	// cmdRefreshByCwd. To make Tail emit, we have to write JSONL into
	// the projects root after the Tail watcher is up.
	now := fixedNow()
	sessionsRoot := t.TempDir()
	repoRoot := t.TempDir() // unused beyond identity
	wt := filepath.Join(repoRoot, "wt-a")

	// Pre-create the session file before Open so the store indexes it.
	sessionID := "00000000-0000-0000-0000-0000feed1001"
	writeJSONLSession(t, sessionsRoot, "proj", sessionID, wt, now.Add(-30*time.Second))

	// Now Open the store. Tail can be started by Start() of the
	// aggregator; we don't pre-open Tail.
	// Re-open using the sessions package.
	store, err := openStoreAt(t, sessionsRoot)
	if err != nil {
		t.Fatalf("openStoreAt: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {{Path: wt, Branch: "feat/a"}},
		},
		statuses: map[string]git.Worktree{
			wt: {Path: wt, Branch: "feat/a", DirtyFiles: 0},
		},
		statusCalls: &atomic.Int64{},
	}

	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot, Name: "repo"}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcs:      fakes.listProcs,
		now:            func() time.Time { return now },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Close()

	sub := a.Subscribe(context.Background())
	// Drain baseline.
	_ = drain(t, sub, 1, time.Second)

	baselineStatusCalls := fakes.statusCalls.Load()

	fakes.mu.Lock()
	fakes.statuses[wt] = git.Worktree{Path: wt, Branch: "feat/a", DirtyFiles: 42}
	fakes.mu.Unlock()

	appendJSONLLine(t, sessionsRoot, "proj", sessionID, wt, now)

	got := drain(t, sub, 1, 3*time.Second)
	if len(got) != 1 {
		t.Fatalf("tail-driven refresh: got %d updates, want 1", len(got))
	}
	if got[0].State.Worktree.DirtyFiles != 42 {
		t.Errorf("DirtyFiles=%d after tail-driven refresh; want 42", got[0].State.Worktree.DirtyFiles)
	}
	if fakes.statusCalls.Load() == baselineStatusCalls {
		t.Errorf("worktreeStatus was not re-invoked by tail-driven refresh")
	}
}

// updatePaths returns the Worktree paths from a slice of Updates.
func updatePaths(us []Update) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.Worktree)
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
