package aggregator

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/sessions"
)

type command struct {
	kind     commandKind
	worktree string
	cwd      string
	sub      chan Update
	done     chan struct{}
}

type commandKind uint8

const (
	cmdRefreshAll commandKind = iota
	cmdRefreshWorktree
	cmdRefreshByCwd
	cmdSubscribe
	cmdUnsubscribe
)

// loop is the state-owner goroutine. It is the only goroutine that
// reads or writes the state map; all other goroutines send commands
// here. ready is closed once the initial refresh has populated state
// and the fsnotify watcher is wired.
func (a *Aggregator) loop(ctx context.Context, ready chan<- struct{}) {
	defer a.wg.Done()

	state := make(map[string]WorktreeState)
	pathToRepo := make(map[string]Repo)
	subscribers := make(map[chan Update]struct{})
	// prErrs tracks the last PR-fetch error observed per repo. Used to
	// dedup user-facing notices: we only broadcast when the error
	// transitions to a new actionable sentinel (ErrNoGH / ErrNotAuthed),
	// not on every poll while it persists.
	prErrs := make(map[string]error)

	a.refreshAll(ctx, state, pathToRepo, subscribers, prErrs, false)

	watcher := a.startWatcher(ctx, state)
	defer func() {
		if watcher != nil {
			_ = watcher.Close()
		}
	}()

	close(ready)

	for {
		select {
		case <-ctx.Done():
			a.closeSubscribers(subscribers)
			return
		case <-a.stop:
			a.closeSubscribers(subscribers)
			return
		case cmd := <-a.cmds:
			a.handleCommand(ctx, cmd, state, pathToRepo, subscribers, prErrs, watcher)
		}
	}
}

func (a *Aggregator) handleCommand(
	ctx context.Context,
	cmd command,
	state map[string]WorktreeState,
	pathToRepo map[string]Repo,
	subscribers map[chan Update]struct{},
	prErrs map[string]error,
	watcher *fsWatcher,
) {
	switch cmd.kind {
	case cmdRefreshAll:
		a.refreshAll(ctx, state, pathToRepo, subscribers, prErrs, true)
		if watcher != nil {
			watcher.sync(state)
		}
	case cmdRefreshWorktree:
		a.refreshOne(ctx, cmd.worktree, state, pathToRepo, subscribers, prErrs, watcher)
	case cmdRefreshByCwd:
		if path := matchWorktreeForCwd(cmd.cwd, state); path != "" {
			a.refreshOne(ctx, path, state, pathToRepo, subscribers, prErrs, watcher)
		}
	case cmdSubscribe:
		subscribers[cmd.sub] = struct{}{}
		for _, path := range slices.Sorted(maps.Keys(state)) {
			a.sendOne(cmd.sub, Update{Worktree: path, State: state[path]})
		}
		// Resend any sticky PR error so a late subscriber sees the
		// same diagnostic an early one would have.
		for _, err := range prErrs {
			if notice := prErrNotice(err); notice != "" {
				a.sendOne(cmd.sub, Update{SystemNotice: notice})
				break
			}
		}
		if cmd.done != nil {
			close(cmd.done)
		}
	case cmdUnsubscribe:
		if _, ok := subscribers[cmd.sub]; ok {
			delete(subscribers, cmd.sub)
			close(cmd.sub)
		}
	}
}

// prErrNotice maps an actionable PR-fetch sentinel to a one-line user
// notice. Returns "" for nil or transient errors (the aggregator
// degrades silently in those cases).
func prErrNotice(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, pr.ErrNoGH):
		return "pr: gh CLI not installed — PR data unavailable"
	case errors.Is(err, pr.ErrNotAuthed):
		return "pr: gh not authenticated — run `gh auth login`"
	}
	return ""
}

// observePRErr records err for repoRoot and broadcasts a one-shot
// SystemNotice when the actionable-error state transitions. broadcast
// must be true to emit; the initial scan suppresses notices to avoid
// a stale notice firing before any subscriber attaches.
func (a *Aggregator) observePRErr(
	repoRoot string,
	err error,
	subscribers map[chan Update]struct{},
	prErrs map[string]error,
	broadcast bool,
) {
	prev := prErrs[repoRoot]
	prErrs[repoRoot] = err
	if !broadcast {
		return
	}
	prevNotice := prErrNotice(prev)
	curNotice := prErrNotice(err)
	if curNotice != "" && curNotice != prevNotice {
		a.broadcast(subscribers, Update{SystemNotice: curNotice})
	}
}

// refreshAll re-walks every repo and updates the state map. When
// broadcast is true, changed worktrees produce subscriber updates.
func (a *Aggregator) refreshAll(
	ctx context.Context,
	state map[string]WorktreeState,
	pathToRepo map[string]Repo,
	subscribers map[chan Update]struct{},
	prErrs map[string]error,
	broadcast bool,
) {
	seen := make(map[string]struct{})
	_ = a.walkAll(ctx,
		func(repo Repo, wt git.Worktree, siblings []string, prs []pr.PR, prStale bool) {
			seen[wt.Path] = struct{}{}
			pathToRepo[wt.Path] = repo
			next := a.buildState(ctx, repo, wt, siblings, prs, prStale)
			prev, had := state[wt.Path]
			state[wt.Path] = next
			if broadcast && (!had || !worktreeStatesEqual(prev, next)) {
				a.broadcast(subscribers, Update{Worktree: wt.Path, State: next})
			}
		},
		func(repoRoot string, err error) {
			a.observePRErr(repoRoot, err, subscribers, prErrs, broadcast)
		},
	)
	for path := range state {
		if _, ok := seen[path]; !ok {
			delete(state, path)
			delete(pathToRepo, path)
		}
	}
}

// refreshOne refreshes one worktree by path. Called from fsnotify and
// tail-driven cmdRefreshByCwd. Drops the worktree from state if its
// status query fails — the next poll will reinstate it if it returns.
func (a *Aggregator) refreshOne(
	ctx context.Context,
	path string,
	state map[string]WorktreeState,
	pathToRepo map[string]Repo,
	subscribers map[chan Update]struct{},
	prErrs map[string]error,
	watcher *fsWatcher,
) {
	repo, ok := pathToRepo[path]
	if !ok {
		return
	}
	full, err := a.cfg.worktreeStatus(ctx, path)
	if err != nil {
		delete(state, path)
		delete(pathToRepo, path)
		if watcher != nil {
			watcher.sync(state)
		}
		return
	}
	// WorktreeStatus does not surface the identity flags ListWorktrees
	// sets (Main, Bare); buildState faithfully copies them off the
	// passed-in wt, so we have to restore them from prev or refreshOne
	// silently flips Main to false and re-arms the prune prompt.
	prev, had := state[path]
	if had {
		full.Main = prev.Worktree.Main
		full.Bare = prev.Worktree.Bare
	}
	prList, prStale, prErr := a.fetchPRs(ctx, repo.Root)
	a.observePRErr(repo.Root, prErr, subscribers, prErrs, true)
	siblings := siblingPaths(pathToRepo, repo)
	next := a.buildState(ctx, repo, full, siblings, prList, prStale)
	state[path] = next
	if !had || !worktreeStatesEqual(prev, next) {
		a.broadcast(subscribers, Update{Worktree: path, State: next})
	}
}

func (a *Aggregator) broadcast(subscribers map[chan Update]struct{}, u Update) {
	for ch := range subscribers {
		a.sendOne(ch, u)
	}
}

func (a *Aggregator) sendOne(ch chan Update, u Update) {
	u.State = cloneStateForBroadcast(u.State)
	select {
	case ch <- u:
	default:
		a.subscriberDrops.Add(1)
	}
}

// cloneStateForBroadcast returns a copy of s with its slice fields
// reallocated. Subscribers must not observe later mutations to the
// loop's internal state map, and Procs/Recent are the only fields
// where the value type is a slice header. Pointer fields (PR, Live)
// reference data that's effectively immutable from the aggregator's
// perspective and don't need to be deep-cloned.
func cloneStateForBroadcast(s WorktreeState) WorktreeState {
	if len(s.Procs) > 0 {
		ps := make([]procs.Process, len(s.Procs))
		copy(ps, s.Procs)
		s.Procs = ps
	}
	if len(s.Recent) > 0 {
		rs := make([]*sessions.Session, len(s.Recent))
		copy(rs, s.Recent)
		s.Recent = rs
	}
	return s
}

func (a *Aggregator) closeSubscribers(subscribers map[chan Update]struct{}) {
	for ch := range subscribers {
		close(ch)
		delete(subscribers, ch)
	}
}

// matchWorktreeForCwd returns the path of the worktree whose path is
// the longest path-aware prefix of cwd, or "" if no worktree matches.
func matchWorktreeForCwd(cwd string, state map[string]WorktreeState) string {
	if cwd == "" {
		return ""
	}
	bestPath := ""
	bestLen := 0
	for path := range state {
		if !pathHasPrefix(cwd, path) {
			continue
		}
		if len(path) > bestLen {
			bestPath = path
			bestLen = len(path)
		}
	}
	return bestPath
}

// longestMatchingPath returns the longest path in paths that is a path-aware
// prefix of cwd, or "" if no path matches. Used to attribute a session or
// process to the deepest containing worktree when worktrees are nested.
func longestMatchingPath(cwd string, paths []string) string {
	if cwd == "" {
		return ""
	}
	bestPath := ""
	bestLen := 0
	for _, p := range paths {
		if !pathHasPrefix(cwd, p) {
			continue
		}
		if len(p) > bestLen {
			bestPath = p
			bestLen = len(p)
		}
	}
	return bestPath
}

// siblingPaths returns every worktree path tracked under repo. Used by
// refreshOne when it lacks the walkAll-derived siblings list.
func siblingPaths(pathToRepo map[string]Repo, repo Repo) []string {
	out := make([]string, 0, len(pathToRepo))
	for p, r := range pathToRepo {
		if r.Root == repo.Root {
			out = append(out, p)
		}
	}
	return out
}
