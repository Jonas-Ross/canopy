package aggregator

import (
	"context"
	"maps"
	"slices"

	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/pr"
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

	a.refreshAll(ctx, state, pathToRepo, subscribers, false)

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
			a.handleCommand(ctx, cmd, state, pathToRepo, subscribers, watcher)
		}
	}
}

func (a *Aggregator) handleCommand(
	ctx context.Context,
	cmd command,
	state map[string]WorktreeState,
	pathToRepo map[string]Repo,
	subscribers map[chan Update]struct{},
	watcher *fsWatcher,
) {
	switch cmd.kind {
	case cmdRefreshAll:
		a.refreshAll(ctx, state, pathToRepo, subscribers, true)
		if watcher != nil {
			watcher.sync(state)
		}
	case cmdRefreshWorktree:
		a.refreshOne(ctx, cmd.worktree, state, pathToRepo, subscribers, watcher)
	case cmdRefreshByCwd:
		if path := matchWorktreeForCwd(cmd.cwd, state); path != "" {
			a.refreshOne(ctx, path, state, pathToRepo, subscribers, watcher)
		}
	case cmdSubscribe:
		subscribers[cmd.sub] = struct{}{}
		for _, path := range slices.Sorted(maps.Keys(state)) {
			a.sendOne(cmd.sub, Update{Worktree: path, State: state[path]})
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

// refreshAll re-walks every repo and updates the state map. When
// broadcast is true, changed worktrees produce subscriber updates.
func (a *Aggregator) refreshAll(
	ctx context.Context,
	state map[string]WorktreeState,
	pathToRepo map[string]Repo,
	subscribers map[chan Update]struct{},
	broadcast bool,
) {
	seen := make(map[string]struct{})
	_ = a.walkAll(ctx, func(repo Repo, wt git.Worktree, prs []pr.PR, prStale bool) {
		seen[wt.Path] = struct{}{}
		pathToRepo[wt.Path] = repo
		next := a.buildState(ctx, repo, wt, prs, prStale)
		prev, had := state[wt.Path]
		state[wt.Path] = next
		if broadcast && (!had || !worktreeStatesEqual(prev, next)) {
			a.broadcast(subscribers, Update{Worktree: wt.Path, State: next})
		}
	})
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
	prList, prStale := a.fetchPRs(ctx, repo.Root)
	next := a.buildState(ctx, repo, full, prList, prStale)
	prev, had := state[path]
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
	select {
	case ch <- u:
	default:
		a.subscriberDrops.Add(1)
	}
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
