// Package aggregator joins git, processes, agent sessions, and PR state
// into a single per-worktree view that the TUI consumes. It owns the
// worktree<->session correlation via CwdPrefix and is the only package
// that knows about all four data sources.
//
// Two entry points: Snapshot for a synchronous one-shot read, and
// Start+Subscribe for a live diff stream backed by fsnotify watches,
// a poll ticker, and the sessions tail.
package aggregator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/sessions"
)

// Aggregator owns the merged state and drives refreshes. The state map
// is owned by a single goroutine (see loop.go); all reads and writes
// flow through chan-based commands to avoid mutex contention with
// subscriber broadcast.
type Aggregator struct {
	cfg Config

	startMu sync.Mutex
	started bool

	cmds chan command

	stop     chan struct{}
	stopOnce sync.Once

	wg sync.WaitGroup

	subscriberDrops atomic.Uint64
}

// New constructs an Aggregator. It does not start the live refresh
// loop; call Start to begin watching and polling. Snapshot works
// without Start.
func New(cfg Config) (*Aggregator, error) {
	if cfg.SessionStore == nil {
		return nil, errors.New("aggregator: SessionStore is required")
	}
	cfg = withDefaults(cfg)

	return &Aggregator{
		cfg:  cfg,
		cmds: make(chan command, 32),
		stop: make(chan struct{}),
	}, nil
}

func withDefaults(cfg Config) Config {
	if cfg.listWorktrees == nil {
		cfg.listWorktrees = git.ListWorktrees
	}
	if cfg.worktreeStatus == nil {
		cfg.worktreeStatus = git.WorktreeStatus
	}
	if cfg.listProcsByPrefixes == nil {
		cfg.listProcsByPrefixes = procs.ListByCwdPrefixes
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	for i, r := range cfg.Repos {
		if r.Name == "" {
			cfg.Repos[i].Name = filepath.Base(r.Root)
		}
	}
	return cfg
}

// visit is one (repo, worktree) tuple captured during walkAll, used to
// defer buildState calls until after a single batched procs snapshot
// has been taken across every worktree in the walk.
type visit struct {
	repo     Repo
	wt       git.Worktree
	siblings []string
	prs      []pr.PR
	prStale  bool
}

// Snapshot returns the current state of every worktree across all
// configured Repos. Synchronous; runs git/procs/pr/sessions queries
// inline. Safe to call before Start.
//
// Per-worktree status failures fall back to the identity-only fields
// from ListWorktrees and do not abort the snapshot. List-worktrees
// failure for a repo aborts the snapshot and is returned wrapped.
func (a *Aggregator) Snapshot(ctx context.Context) ([]WorktreeState, error) {
	var visits []visit
	err := a.walkAll(ctx, func(repo Repo, wt git.Worktree, siblings []string, prs []pr.PR, prStale bool) {
		visits = append(visits, visit{repo, wt, siblings, prs, prStale})
	}, nil)
	if err != nil {
		return nil, err
	}
	procsByPrefix := a.procsSnapshot(ctx, visitPrefixes(visits))
	out := make([]WorktreeState, 0, len(visits))
	for _, v := range visits {
		out = append(out, a.buildState(ctx, v.repo, v.wt, v.siblings, v.prs, v.prStale, procsByPrefix))
	}
	return out, nil
}

// visitPrefixes extracts the worktree paths from a slice of visits,
// preserving order. Used to feed procsSnapshot.
func visitPrefixes(visits []visit) []string {
	prefixes := make([]string, len(visits))
	for i, v := range visits {
		prefixes[i] = v.wt.Path
	}
	return prefixes
}

// walkAll iterates every (repo, worktree) pair across configured Repos,
// calling visit for each with the current PR list, stale bool, and the
// full set of sibling worktree paths for that repo (used by buildState
// to attribute prefix-matched sessions/procs to the deepest worktree).
// The visit callback runs synchronously on the caller goroutine. A
// list-worktrees failure aborts and is returned wrapped.
//
// onPRErr (optional) is invoked once per repo with the PR-fetch error
// (or nil) so the caller can track per-repo PR state across walks.
func (a *Aggregator) walkAll(ctx context.Context, visit func(repo Repo, wt git.Worktree, siblings []string, prs []pr.PR, prStale bool), onPRErr func(repoRoot string, err error)) error {
	for _, repo := range a.cfg.Repos {
		wts, err := a.cfg.listWorktrees(ctx, repo.Root)
		if err != nil {
			return fmt.Errorf("aggregator: list worktrees for %s: %w", repo.Root, err)
		}
		siblings := make([]string, 0, len(wts))
		for _, wt := range wts {
			siblings = append(siblings, wt.Path)
		}
		prList, prStale, prErr := a.fetchPRs(ctx, repo.Root)
		if onPRErr != nil {
			onPRErr(repo.Root, prErr)
		}
		for _, wt := range wts {
			visit(repo, wt, siblings, prList, prStale)
		}
	}
	return nil
}

// procsSnapshot is a one-shot batched procs walk across every prefix
// the caller cares about. Failures are soft-degraded: an error returns
// an empty map so buildState's bucket lookup simply finds no entry
// and leaves Procs as the zero slice. Same shape as the per-pid
// silent-skip behavior in procs/.
func (a *Aggregator) procsSnapshot(ctx context.Context, prefixes []string) map[string][]procs.Process {
	if len(prefixes) == 0 {
		return map[string][]procs.Process{}
	}
	m, err := a.cfg.listProcsByPrefixes(ctx, prefixes)
	if err != nil {
		return map[string][]procs.Process{}
	}
	return m
}

// buildState joins git/pr/procs/session data for one worktree. siblings is
// the list of every worktree path in the same repo — used to attribute a
// prefix-matched session or process to the deepest containing worktree
// (e.g. a session under /repo/.worktrees/feat must NOT also appear under
// /repo).
func (a *Aggregator) buildState(ctx context.Context, repo Repo, wt git.Worktree, siblings []string, prList []pr.PR, prStale bool, procsByPrefix map[string][]procs.Process) WorktreeState {
	state := WorktreeState{
		Repo:      repo,
		Worktree:  wt,
		UpdatedAt: a.cfg.now(),
		Procs:     []procs.Process{},
	}

	if full, err := a.cfg.worktreeStatus(ctx, wt.Path); err == nil {
		// WorktreeStatus does not surface the identity flags that
		// ListWorktrees sets from porcelain output.
		full.Bare = wt.Bare
		full.Main = wt.Main
		state.Worktree = full
	}

	if ps, ok := procsByPrefix[state.Worktree.Path]; ok {
		for _, p := range ps {
			if longestMatchingPath(p.Cwd, siblings) == state.Worktree.Path {
				state.Procs = append(state.Procs, p)
			}
		}
	}

	if state.Worktree.Branch != "" {
		for i := range prList {
			if prList[i].HeadBranch == state.Worktree.Branch {
				p := prList[i]
				state.PR = &p
				state.PRStale = prStale
				break
			}
		}
	}

	rawSess := a.cfg.SessionStore.SessionsByCwdPrefix(state.Worktree.Path)
	sess := rawSess[:0]
	// A session is attributed to the deepest worktree containing its most
	// recent cwd (Cwds is observation-ordered; last entry is current). This
	// prevents a session that started in /repo and later moved into
	// /repo/.worktrees/feat from showing on both worktrees.
	for _, s := range rawSess {
		if len(s.Cwds) == 0 {
			continue
		}
		lastCwd := s.Cwds[len(s.Cwds)-1]
		if longestMatchingPath(lastCwd, siblings) == state.Worktree.Path {
			sess = append(sess, s)
		}
	}
	if len(sess) > 0 {
		recent := sess
		if len(recent) > RecentSessionsLimit {
			recent = recent[:RecentSessionsLimit]
		}
		state.Recent = recent
		now := a.cfg.now()
		for _, s := range sess {
			if now.Sub(s.UpdatedAt) < LiveWindow {
				state.Live = s
				break
			}
		}
	}

	return state
}

// fetchPRs returns the PR list for repoRoot, the stale flag, and any
// error surfaced by pr.Cache.Get (no cached fallback available). A
// nil PRCache yields a nil error. Callers track the error to surface
// user-actionable failures (ErrNoGH, ErrNotAuthed) to the TUI.
func (a *Aggregator) fetchPRs(ctx context.Context, repoRoot string) ([]pr.PR, bool, error) {
	if a.cfg.PRCache == nil {
		return nil, false, nil
	}
	prs, stale, err := a.cfg.PRCache.Get(ctx, repoRoot)
	if err != nil {
		return nil, false, err
	}
	return prs, stale, nil
}

// Start launches the state-owner goroutine, the fsnotify watcher,
// the poll ticker, and the sessions.Tail consumer. Calling Start
// twice returns an error. The provided ctx scopes every background
// goroutine — closing it (or calling Close) stops everything.
func (a *Aggregator) Start(ctx context.Context) error {
	a.startMu.Lock()
	if a.started {
		a.startMu.Unlock()
		return errors.New("aggregator: already started")
	}
	a.started = true
	a.startMu.Unlock()

	// Set up the sessions.Tail watcher synchronously so it is seeded
	// (and thus capturing file appends) before Start returns.
	var tail <-chan sessions.TailItem
	if a.cfg.SessionStore != nil {
		t, err := a.cfg.SessionStore.Tail(ctx)
		if err == nil {
			tail = t
		}
	}

	// The loop runs its initial refreshAll synchronously and signals
	// ready, so anyone who subscribes after Start sees a populated
	// baseline rather than racing the first refresh.
	loopReady := make(chan struct{})
	a.wg.Add(1)
	go a.loop(ctx, loopReady)
	<-loopReady

	a.wg.Add(1)
	go a.poll(ctx)

	a.wg.Add(1)
	go a.tailConsumer(ctx, tail)

	return nil
}

// Close stops all goroutines and closes any subscriber channels.
// Safe to call multiple times.
func (a *Aggregator) Close() {
	a.stopOnce.Do(func() { close(a.stop) })
	a.wg.Wait()
}

// Refresh forces a re-scan of all sources. Returns immediately after
// kicking the refresh; updates flow through Subscribe. Coalesced —
// repeated calls while the channel is full are dropped silently.
func (a *Aggregator) Refresh() {
	select {
	case a.cmds <- command{kind: cmdRefreshAll}:
	case <-a.stop:
	default:
	}
}

// Subscribe returns a channel that emits an Update per worktree
// change. Each subscriber receives a baseline Update for every known
// worktree first, then incremental updates. Bounded buffer (64);
// drops new events on full (drop count visible via Stats). The channel
// closes when ctx is cancelled or the aggregator is Closed.
//
// Subscribe before Start returns an immediately-closed channel.
func (a *Aggregator) Subscribe(ctx context.Context) <-chan Update {
	ch := make(chan Update, subscriberBufferSize)

	a.startMu.Lock()
	started := a.started
	a.startMu.Unlock()
	if !started {
		close(ch)
		return ch
	}

	// Reserve a wg slot up front. Done() runs either on the stop
	// short-circuit paths below or via the unsubscribe goroutine's
	// defer. This must happen before any chance of Close()'s Wait()
	// observing a zero counter and returning early.
	a.wg.Add(1)

	done := make(chan struct{})
	select {
	case a.cmds <- command{kind: cmdSubscribe, sub: ch, done: done}:
	case <-a.stop:
		a.wg.Done()
		close(ch)
		return ch
	}

	// Block until the loop has registered the subscriber so the
	// baseline emission is observable before Subscribe returns.
	select {
	case <-done:
	case <-a.stop:
		a.wg.Done()
		close(ch)
		return ch
	}

	go func() {
		defer a.wg.Done()
		select {
		case <-ctx.Done():
		case <-a.stop:
			return
		}
		select {
		case a.cmds <- command{kind: cmdUnsubscribe, sub: ch}:
		case <-a.stop:
		}
	}()

	return ch
}

// Stats returns observable counters.
func (a *Aggregator) Stats() Stats {
	return Stats{SubscriberDrops: a.subscriberDrops.Load()}
}
