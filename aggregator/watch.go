package aggregator

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/jonasross/canopy/sessions"
)

const fsEventDebounce = 100 * time.Millisecond

// fsWatcher wraps fsnotify with the set of worktree paths it currently
// watches. It is owned by the loop goroutine; the watch goroutine only
// reads from the fsnotify event channel and translates events into
// refreshWorktree commands.
type fsWatcher struct {
	w *fsnotify.Watcher

	watched       map[string][]string
	dirToWorktree map[string]string

	debouncer *debouncer
}

// startWatcher creates the watcher (if fsnotify is healthy on this
// platform), registers watches for every currently-known worktree, and
// spawns the event-handling goroutine. Returns nil if fsnotify init
// fails — the aggregator keeps running on the poll ticker alone.
func (a *Aggregator) startWatcher(ctx context.Context, state map[string]WorktreeState) *fsWatcher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	fw := &fsWatcher{
		w:             w,
		watched:       make(map[string][]string),
		dirToWorktree: make(map[string]string),
	}
	fw.debouncer = newDebouncer(fsEventDebounce, func(path string) {
		a.sendCommand(command{kind: cmdRefreshWorktree, worktree: path})
	})
	fw.sync(state)

	a.wg.Add(1)
	go a.watchLoop(ctx, fw)

	return fw
}

func (fw *fsWatcher) sync(state map[string]WorktreeState) {
	if fw == nil || fw.w == nil {
		return
	}
	for path := range state {
		if _, ok := fw.watched[path]; ok {
			continue
		}
		paths := watchPathsFor(path)
		added := make([]string, 0, len(paths))
		for _, p := range paths {
			if err := fw.w.Add(p); err == nil {
				added = append(added, p)
				fw.dirToWorktree[p] = path
			}
		}
		fw.watched[path] = added
	}
	for path, added := range fw.watched {
		if _, ok := state[path]; ok {
			continue
		}
		for _, p := range added {
			_ = fw.w.Remove(p)
			delete(fw.dirToWorktree, p)
		}
		delete(fw.watched, path)
	}
}

// Close releases the underlying fsnotify resources. Idempotent.
func (fw *fsWatcher) Close() error {
	if fw == nil || fw.w == nil {
		return nil
	}
	if fw.debouncer != nil {
		fw.debouncer.stop()
	}
	return fw.w.Close()
}

// watchPathsFor returns the set of paths under the worktree that the
// aggregator wants to watch. fsnotify is non-recursive, so we add a
// watch per directory and rely on Create events for new ref files.
//
// Paths that don't exist are filtered by fsnotify.Add returning an
// error; sync() tolerates that silently.
func watchPathsFor(worktreePath string) []string {
	gitDir := filepath.Join(worktreePath, ".git")
	return []string{
		gitDir,
		filepath.Join(gitDir, "refs", "heads"),
		worktreePath,
	}
}

func (a *Aggregator) watchLoop(ctx context.Context, fw *fsWatcher) {
	defer a.wg.Done()
	if fw == nil || fw.w == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		case ev, ok := <-fw.w.Events:
			if !ok {
				return
			}
			path := fw.resolveWorktreePath(ev.Name)
			if path == "" {
				continue
			}
			fw.debouncer.trip(path)
		case _, ok := <-fw.w.Errors:
			if !ok {
				return
			}
			// fsnotify-level errors are non-fatal; the poll ticker is
			// the safety net.
		}
	}
}

// resolveWorktreePath maps an fsnotify event path back to a worktree
// the aggregator is tracking. Exact match wins; otherwise we pick the
// longest matching watched-directory prefix.
func (fw *fsWatcher) resolveWorktreePath(eventPath string) string {
	if eventPath == "" {
		return ""
	}
	if wt, ok := fw.dirToWorktree[eventPath]; ok {
		return wt
	}
	bestWT := ""
	bestLen := 0
	for dir, wt := range fw.dirToWorktree {
		if !pathHasPrefix(eventPath, dir) {
			continue
		}
		if len(dir) > bestLen {
			bestWT = wt
			bestLen = len(dir)
		}
	}
	return bestWT
}

// sendCommand is the non-blocking sender used by background helpers.
// We never want to wedge fsnotify or the tail consumer on a backed-up
// command channel — the next periodic refresh will re-converge.
func (a *Aggregator) sendCommand(cmd command) {
	select {
	case a.cmds <- cmd:
	case <-a.stop:
	default:
	}
}

// poll drives the refresh ticker.
func (a *Aggregator) poll(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		case <-t.C:
			a.sendCommand(command{kind: cmdRefreshAll})
		}
	}
}

// tailConsumer reads from the sessions store's Tail channel and maps
// each event to a worktree refresh. tail may be nil — that happens when
// no SessionStore is configured or its Tail returned an error.
func (a *Aggregator) tailConsumer(ctx context.Context, tail <-chan sessions.TailItem) {
	defer a.wg.Done()
	if tail == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		case item, ok := <-tail:
			if !ok {
				return
			}
			if item.Err != nil {
				continue
			}
			a.handleTailEvent(item.Event.SessionID)
		}
	}
}

func (a *Aggregator) handleTailEvent(sessionID string) {
	if a.cfg.SessionStore == nil {
		return
	}
	sess, err := a.cfg.SessionStore.Session(sessionID)
	if err != nil {
		return
	}
	for _, cwd := range sess.Cwds {
		if cwd == "" {
			continue
		}
		if !a.cwdUnderAnyRepo(cwd) {
			continue
		}
		a.sendCommand(command{kind: cmdRefreshByCwd, cwd: cwd})
	}
}

func (a *Aggregator) cwdUnderAnyRepo(cwd string) bool {
	for _, r := range a.cfg.Repos {
		if pathHasPrefix(cwd, r.Root) {
			return true
		}
	}
	return false
}

// pathHasPrefix is CwdPrefix-style matching with a path-boundary check
// so /repo/wt-a does not match /repo/wt-ab. Exact equality also counts.
func pathHasPrefix(cwd, prefix string) bool {
	if cwd == prefix {
		return true
	}
	if !strings.HasPrefix(cwd, prefix) {
		return false
	}
	rest := cwd[len(prefix):]
	return len(rest) > 0 && rest[0] == filepath.Separator
}

// debouncer coalesces rapid events for the same key. Each key gets at
// most one pending timer; tripping while a timer is pending resets it
// so a burst fires once after the trailing event quiesces.
type debouncer struct {
	mu     sync.Mutex
	timers map[string]*time.Timer
	delay  time.Duration
	fire   func(path string)
}

func newDebouncer(delay time.Duration, fire func(string)) *debouncer {
	return &debouncer{
		timers: make(map[string]*time.Timer),
		delay:  delay,
		fire:   fire,
	}
}

func (d *debouncer) trip(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timers == nil {
		return
	}
	if t, ok := d.timers[path]; ok {
		t.Reset(d.delay)
		return
	}
	d.timers[path] = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		delete(d.timers, path)
		d.mu.Unlock()
		d.fire(path)
	})
}

func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, t := range d.timers {
		t.Stop()
	}
	d.timers = nil
}
