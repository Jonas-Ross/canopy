package aggregator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/sessions"
)

// fixedNow returns a deterministic clock at a known wall time. Most
// tests fix the clock at this instant and place fixture session
// mtimes relative to it.
func fixedNow() time.Time {
	return time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
}

// fakeSources bundles the per-test programmable seams.
type fakeSources struct {
	mu sync.Mutex

	worktrees     map[string][]git.Worktree  // by repo root
	statuses      map[string]git.Worktree    // by worktree path
	statusErrors  map[string]error           // by worktree path
	listWtErrors  map[string]error           // by repo root
	procs         map[string][]procs.Process // by prefix
	procsErr      error
	procsByPrefix func(prefix string) ([]procs.Process, error)
	listWtCalls   *atomic.Int64 // optional counter; tests provide it to assert invocation
	statusCalls   *atomic.Int64
	procsCalls    *atomic.Int64 // optional counter; tests provide it to assert invocation
}

func (f *fakeSources) listWorktrees(ctx context.Context, repoRoot string) ([]git.Worktree, error) {
	if f.listWtCalls != nil {
		f.listWtCalls.Add(1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.listWtErrors[repoRoot]; ok {
		return nil, err
	}
	return append([]git.Worktree(nil), f.worktrees[repoRoot]...), nil
}

func (f *fakeSources) worktreeStatus(ctx context.Context, path string) (git.Worktree, error) {
	if f.statusCalls != nil {
		f.statusCalls.Add(1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.statusErrors[path]; ok {
		return git.Worktree{}, err
	}
	if wt, ok := f.statuses[path]; ok {
		return wt, nil
	}
	// No fixture: synthesize a minimal Worktree by copying identity
	// fields from the matching ListWorktrees entry. This mirrors the
	// real WorktreeStatus, which always returns at least a populated
	// branch when the worktree is on one.
	for _, wts := range f.worktrees {
		for _, wt := range wts {
			if wt.Path == path {
				return wt, nil
			}
		}
	}
	return git.Worktree{Path: path}, nil
}

func (f *fakeSources) listProcsByPrefixes(ctx context.Context, prefixes []string) (map[string][]procs.Process, error) {
	if f.procsCalls != nil {
		f.procsCalls.Add(1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.procsErr != nil {
		return nil, f.procsErr
	}
	out := make(map[string][]procs.Process, len(prefixes))
	for _, p := range prefixes {
		if f.procsByPrefix != nil {
			ps, err := f.procsByPrefix(p)
			if err != nil {
				return nil, err
			}
			out[p] = ps
			continue
		}
		if ps := f.procs[p]; len(ps) > 0 {
			out[p] = append([]procs.Process(nil), ps...)
		} else {
			out[p] = []procs.Process{}
		}
	}
	return out, nil
}

// writeJSONLSession writes one fixture JSONL file under root with a
// single user line carrying the given sessionID and cwd. The file's
// ModTime is then set to updatedAt so sessions.Open derives Session.
// UpdatedAt from it.
func writeJSONLSession(t *testing.T, root string, projectDirName, sessionID, cwd string, updatedAt time.Time) string {
	t.Helper()
	dir := filepath.Join(root, projectDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	ts := updatedAt.UTC().Format(time.RFC3339)
	line := fmt.Sprintf(
		`{"type":"user","uuid":"u-%s","parentUuid":null,"sessionId":%q,"timestamp":%q,"cwd":%q,"gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hello"}}`+"\n",
		sessionID, sessionID, ts, cwd,
	)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, updatedAt, updatedAt); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

// openTestSessionStore opens a sessions.Store rooted under a freshly
// created temp dir. The returned root is where additional fixture
// files can be written before opening.
func openTestSessionStore(t *testing.T, fixtures func(root string)) *sessions.Store {
	t.Helper()
	root := t.TempDir()
	fixtures(root)
	store, err := sessions.Open(root)
	if err != nil {
		t.Fatalf("sessions.Open(%s): %v", root, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// openStoreAt is openTestSessionStore without the fixture callback —
// useful when the caller has already populated the root directly.
func openStoreAt(t *testing.T, root string) (*sessions.Store, error) {
	t.Helper()
	return sessions.Open(root)
}

// appendJSONLLine appends one user line to an existing session file
// so the sessions.Tail watcher emits a fresh event.
func appendJSONLLine(t *testing.T, root, projectDirName, sessionID, cwd string, ts time.Time) {
	t.Helper()
	path := filepath.Join(root, projectDirName, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer f.Close()
	tsStr := ts.UTC().Format(time.RFC3339)
	line := fmt.Sprintf(
		`{"type":"user","uuid":"u-tail-%d","parentUuid":null,"sessionId":%q,"timestamp":%q,"cwd":%q,"gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"more"}}`+"\n",
		ts.UnixNano(), sessionID, tsStr, cwd,
	)
	if _, err := f.Write([]byte(line)); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestSnapshot_SingleRepo_FullJoin(t *testing.T) {
	now := fixedNow()
	repoRoot := t.TempDir()
	wt1 := filepath.Join(repoRoot, "wt-a")
	wt2 := filepath.Join(repoRoot, "wt-b")

	store := openTestSessionStore(t, func(root string) {
		writeJSONLSession(t, root, "wt-a", "00000000-0000-0000-0000-000000000a01", wt1, now.Add(-30*time.Second))
		writeJSONLSession(t, root, "wt-b", "00000000-0000-0000-0000-000000000b01", wt2, now.Add(-1*time.Hour))
	})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: wt1, Branch: "feat/a"},
				{Path: wt2, Branch: "feat/b"},
			},
		},
		statuses: map[string]git.Worktree{
			wt1: {Path: wt1, Branch: "feat/a", DirtyFiles: 3, Ahead: 2, Behind: 0, HasUpstream: true, LastCommit: git.Commit{Hash: "abc", Subject: "do a thing"}},
			wt2: {Path: wt2, Branch: "feat/b", DirtyFiles: 0, Ahead: 0, Behind: 5, HasUpstream: true},
		},
		procs: map[string][]procs.Process{
			wt1: {{Pid: 100, Command: "claude", Cwd: wt1}},
			wt2: {},
		},
	}

	prCache := installFakePRCache(t, map[string][]pr.PR{
		repoRoot: {
			{Number: 42, Title: "Add a thing", HeadBranch: "feat/a", State: "OPEN", CIRollup: "SUCCESS"},
			{Number: 43, Title: "Add b thing", HeadBranch: "feat/b", State: "OPEN"},
		},
	}, false, nil)

	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot, Name: "repo"}},
		SessionStore:   store,
		PRCache:        prCache,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})

	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Snapshot: got %d states, want 2", len(got))
	}

	byPath := map[string]WorktreeState{}
	for _, s := range got {
		byPath[s.Worktree.Path] = s
	}
	a1 := byPath[wt1]
	if a1.Worktree.DirtyFiles != 3 || a1.Worktree.Ahead != 2 {
		t.Errorf("wt-a status not joined: %+v", a1.Worktree)
	}
	if a1.PR == nil || a1.PR.Number != 42 {
		t.Errorf("wt-a PR not joined: %+v", a1.PR)
	}
	if len(a1.Procs) != 1 || a1.Procs[0].Pid != 100 {
		t.Errorf("wt-a procs not joined: %+v", a1.Procs)
	}
	if a1.Live == nil {
		t.Errorf("wt-a Live nil; want session within window")
	}
	if len(a1.Recent) == 0 {
		t.Errorf("wt-a Recent empty")
	}
	if a1.Repo.Name != "repo" {
		t.Errorf("wt-a Repo.Name=%q, want %q", a1.Repo.Name, "repo")
	}

	b1 := byPath[wt2]
	if b1.PR == nil || b1.PR.Number != 43 {
		t.Errorf("wt-b PR not joined: %+v", b1.PR)
	}
	if b1.Live != nil {
		t.Errorf("wt-b Live=%v; want nil (session is 1h old)", b1.Live)
	}
	if len(b1.Recent) == 0 {
		t.Errorf("wt-b Recent empty; session exists outside live window")
	}
	if len(b1.Procs) != 0 {
		t.Errorf("wt-b Procs=%v, want empty slice", b1.Procs)
	}
	if b1.Procs == nil {
		t.Errorf("wt-b Procs is nil; should be empty slice")
	}
}

func TestSnapshot_NoPR(t *testing.T) {
	now := fixedNow()
	repoRoot := t.TempDir()
	wt := filepath.Join(repoRoot, "wt")

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "feat/none"}}},
	}
	prCache := installFakePRCache(t, map[string][]pr.PR{repoRoot: {}}, false, nil)

	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		PRCache:        prCache,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 state, got %d", len(got))
	}
	if got[0].PR != nil {
		t.Errorf("PR=%v; want nil", got[0].PR)
	}
	if got[0].PRStale {
		t.Errorf("PRStale=true; want false")
	}
}

func TestSnapshot_NoLiveSession(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	wt := "/repo/wt"

	store := openTestSessionStore(t, func(root string) {
		writeJSONLSession(t, root, "wt", "00000000-0000-0000-0000-000000000001", wt, now.Add(-10*time.Minute))
	})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "main"}}},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got[0].Live != nil {
		t.Errorf("Live=%v, want nil", got[0].Live)
	}
	if len(got[0].Recent) == 0 {
		t.Errorf("Recent empty; want populated")
	}
}

func TestSnapshot_NoSessionsAtAll(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	wt := "/repo/wt"

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "main"}}},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got[0].Recent) != 0 {
		t.Errorf("Recent=%v; want empty/nil", got[0].Recent)
	}
	if got[0].Live != nil {
		t.Errorf("Live=%v; want nil", got[0].Live)
	}
}

func TestSnapshot_NoProcs(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	wt := "/repo/wt"

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "main"}}},
		// No procs entry for wt → returns nil slice → we normalize.
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, _ := a.Snapshot(context.Background())
	if got[0].Procs == nil {
		t.Errorf("Procs is nil; want empty slice")
	}
	if len(got[0].Procs) != 0 {
		t.Errorf("Procs=%v; want empty", got[0].Procs)
	}
}

func TestSnapshot_NestedCwdMatchesCorrectly(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	wtA := "/repo/worktree-a"
	wtAB := "/repo/worktree-ab"

	store := openTestSessionStore(t, func(root string) {
		// Session under nested subdir of worktree-a — must NOT match
		// worktree-ab even though /repo/worktree-a is a string prefix
		// of /repo/worktree-ab.
		writeJSONLSession(t, root, "a-nested", "00000000-0000-0000-0000-000000000a01", wtA+"/sub/dir", now.Add(-10*time.Second))
		// Session under worktree-ab.
		writeJSONLSession(t, root, "ab", "00000000-0000-0000-0000-00000000ab01", wtAB, now.Add(-10*time.Second))
	})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: wtA, Branch: "feat/a"},
				{Path: wtAB, Branch: "feat/ab"},
			},
		},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	byPath := map[string]WorktreeState{}
	for _, s := range got {
		byPath[s.Worktree.Path] = s
	}
	// sessions.Store uses pure string-prefix matching; the load-bearing
	// invariant here is that AB's session lands on AB and A's nested
	// session lands on A — neither is lost or misattributed.
	if !containsSessionByCwd(byPath[wtAB].Recent, wtAB) {
		t.Errorf("worktree-ab missing its own session; got %+v", byPath[wtAB].Recent)
	}
	if !containsSessionByCwd(byPath[wtA].Recent, wtA+"/sub/dir") {
		t.Errorf("worktree-a missing nested session %s; got %+v", wtA+"/sub/dir", byPath[wtA].Recent)
	}
}

// writeJSONLSessionMultiCwd writes a JSONL session with two cwds (first and
// last conversation lines). Used to exercise the case where a Claude session
// starts in /repo and later moves into /repo/.worktrees/feat.
func writeJSONLSessionMultiCwd(t *testing.T, root, projectDirName, sessionID, firstCwd, lastCwd string, updatedAt time.Time) string {
	t.Helper()
	dir := filepath.Join(root, projectDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	first := updatedAt.Add(-30 * time.Second).UTC().Format(time.RFC3339)
	last := updatedAt.UTC().Format(time.RFC3339)
	line := func(uid, ts, cwd string) string {
		return fmt.Sprintf(
			`{"type":"user","uuid":%q,"parentUuid":null,"sessionId":%q,"timestamp":%q,"cwd":%q,"gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hello"}}`+"\n",
			uid, sessionID, ts, cwd,
		)
	}
	content := line("u-1-"+sessionID, first, firstCwd) + line("u-2-"+sessionID, last, lastCwd)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, updatedAt, updatedAt); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

// TestSnapshot_SessionMovedIntoNestedWorktreeOnlyAttributesToLast pins the
// fix for the user-visible bug where a Claude session that started in /repo
// and later moved into /repo/.worktrees/feat was being attributed to BOTH
// worktrees. The session belongs only to its most-recent cwd's worktree.
func TestSnapshot_SessionMovedIntoNestedWorktreeOnlyAttributesToLast(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	mainWT := "/repo"
	nestedWT := "/repo/.worktrees/feat"

	store := openTestSessionStore(t, func(root string) {
		// Session started in /repo, later moved into the nested worktree.
		writeJSONLSessionMultiCwd(t, root, "moved", "00000000-0000-0000-0000-000000000099",
			mainWT, nestedWT, now.Add(-10*time.Second))
	})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: mainWT, Branch: "main"},
				{Path: nestedWT, Branch: "feat"},
			},
		},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	byPath := map[string]WorktreeState{}
	for _, s := range got {
		byPath[s.Worktree.Path] = s
	}

	if byPath[mainWT].Live != nil {
		t.Errorf("main worktree Live=%+v; want nil (session's last cwd is nested)", byPath[mainWT].Live)
	}
	if len(byPath[mainWT].Recent) != 0 {
		t.Errorf("main worktree Recent=%+v; want empty (session's last cwd is nested)", byPath[mainWT].Recent)
	}
	if byPath[nestedWT].Live == nil {
		t.Errorf("nested worktree Live=nil; want the moved session")
	}
}

// TestSnapshot_NestedWorktreeSessionsAttributeToDeepest pins the fix for
// the canopy bug where a worktree at /repo/.worktrees/feat (whose path is
// nested inside the main worktree at /repo) caused its session to also
// appear under /repo via pure-string prefix matching. The deepest matching
// worktree must win.
func TestSnapshot_NestedWorktreeSessionsAttributeToDeepest(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	mainWT := "/repo"
	nestedWT := "/repo/.worktrees/feat"

	store := openTestSessionStore(t, func(root string) {
		// Live session in the nested worktree — must NOT also show on /repo.
		writeJSONLSession(t, root, "nested", "00000000-0000-0000-0000-000000000001", nestedWT, now.Add(-10*time.Second))
		// Another older session strictly in /repo (cwd is the main worktree
		// itself, no .worktrees/ subpath) — this one should land on /repo.
		writeJSONLSession(t, root, "main", "00000000-0000-0000-0000-000000000002", mainWT, now.Add(-2*time.Hour))
	})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: mainWT, Branch: "main"},
				{Path: nestedWT, Branch: "feat"},
			},
		},
		procs: map[string][]procs.Process{
			// listProcsByPrefixes is called once with both prefixes. The /repo
			// bucket contains both processes (a prefix match); the
			// /repo/.worktrees/feat bucket contains only the nested one.
			// buildState must filter so the nested process lands only on the
			// nested worktree, not on /repo.
			mainWT:   {{Pid: 100, Cwd: mainWT, Command: "shell"}, {Pid: 200, Cwd: nestedWT, Command: "claude"}},
			nestedWT: {{Pid: 200, Cwd: nestedWT, Command: "claude"}},
		},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	byPath := map[string]WorktreeState{}
	for _, s := range got {
		byPath[s.Worktree.Path] = s
	}

	if !containsSessionByCwd(byPath[nestedWT].Recent, nestedWT) {
		t.Errorf("nested worktree missing its own session; got %+v", byPath[nestedWT].Recent)
	}
	if containsSessionByCwd(byPath[mainWT].Recent, nestedWT) {
		t.Errorf("main worktree incorrectly attributed nested-worktree session; got %+v", byPath[mainWT].Recent)
	}
	if !containsSessionByCwd(byPath[mainWT].Recent, mainWT) {
		t.Errorf("main worktree missing its own (cwd==/repo) session; got %+v", byPath[mainWT].Recent)
	}

	if byPath[nestedWT].Live == nil {
		t.Errorf("nested worktree Live=nil; want the live nested session")
	}
	if byPath[mainWT].Live != nil {
		t.Errorf("main worktree Live=%+v; want nil (its only session is 2h old)", byPath[mainWT].Live)
	}

	// Procs: only pid 100 (shell, cwd=/repo) should land on main. Pid 200
	// (claude, cwd=/repo/.worktrees/feat) belongs to the nested worktree.
	hasPid := func(ps []procs.Process, pid int) bool {
		for _, p := range ps {
			if p.Pid == pid {
				return true
			}
		}
		return false
	}
	if hasPid(byPath[mainWT].Procs, 200) {
		t.Errorf("main worktree incorrectly attributed nested-worktree process (pid 200); got %+v", byPath[mainWT].Procs)
	}
	if !hasPid(byPath[mainWT].Procs, 100) {
		t.Errorf("main worktree missing its own process (pid 100); got %+v", byPath[mainWT].Procs)
	}
	if !hasPid(byPath[nestedWT].Procs, 200) {
		t.Errorf("nested worktree missing its claude process (pid 200); got %+v", byPath[nestedWT].Procs)
	}
}

func containsSessionByCwd(sess []*sessions.Session, cwd string) bool {
	for _, s := range sess {
		for _, c := range s.Cwds {
			if c == cwd {
				return true
			}
		}
	}
	return false
}

func TestSnapshot_DarwinProcsUnsupported(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	wt := "/repo/wt"

	store := openTestSessionStore(t, func(root string) {})
	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "main"}}},
		procsErr:  procs.ErrUnsupported,
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got[0].Procs == nil || len(got[0].Procs) != 0 {
		t.Errorf("Procs=%v; want empty slice", got[0].Procs)
	}
}

func TestSnapshot_PRStale(t *testing.T) {
	now := fixedNow()
	repoRoot := t.TempDir()
	wt := filepath.Join(repoRoot, "wt")

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "feat/x"}}},
	}
	prCache := installFakePRCache(t, map[string][]pr.PR{
		repoRoot: {{Number: 1, HeadBranch: "feat/x", State: "OPEN"}},
	}, true, nil)

	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		PRCache:        prCache,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, _ := a.Snapshot(context.Background())
	if got[0].PR == nil || got[0].PR.Number != 1 {
		t.Fatalf("PR not joined: %+v", got[0].PR)
	}
	if !got[0].PRStale {
		t.Errorf("PRStale=false; want true")
	}
}

func TestSnapshot_GhNotInstalled(t *testing.T) {
	now := fixedNow()
	repoRoot := t.TempDir()
	wt := filepath.Join(repoRoot, "wt")

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "feat/x"}}},
	}
	prCache := installFakePRCache(t, nil, false, pr.ErrNoGH)

	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		PRCache:        prCache,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot returned error: %v (want degrade silently)", err)
	}
	if got[0].PR != nil {
		t.Errorf("PR=%v; want nil under ErrNoGH", got[0].PR)
	}
}

func TestSnapshot_PRCacheNil(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	wt := "/repo/wt"

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{repoRoot: {{Path: wt, Branch: "feat/x"}}},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		PRCache:        nil,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, _ := a.Snapshot(context.Background())
	if got[0].PR != nil {
		t.Errorf("PR=%v; want nil when PRCache is nil", got[0].PR)
	}
	if got[0].PRStale {
		t.Errorf("PRStale=true; want false")
	}
}

func TestSnapshot_ListWorktreesFailure(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"

	store := openTestSessionStore(t, func(root string) {})

	want := errors.New("git: list worktrees: boom")
	fakes := &fakeSources{
		listWtErrors: map[string]error{repoRoot: want},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	_, err := a.Snapshot(context.Background())
	if err == nil {
		t.Fatalf("Snapshot: got nil, want error")
	}
	if !errors.Is(err, want) {
		t.Errorf("Snapshot err=%v; want wrap of %v", err, want)
	}
}

func TestSnapshot_PerWorktreeStatusFailure(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	wt1 := "/repo/wt-a"
	wt2 := "/repo/wt-b"

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: wt1, Branch: "feat/a"},
				{Path: wt2, Branch: "feat/b"},
			},
		},
		statuses: map[string]git.Worktree{
			wt2: {Path: wt2, Branch: "feat/b", DirtyFiles: 7},
		},
		statusErrors: map[string]error{
			wt1: errors.New("git: status: boom"),
		},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: unexpected err %v (per-worktree failures should not abort)", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 states, got %d", len(got))
	}
	byPath := map[string]WorktreeState{}
	for _, s := range got {
		byPath[s.Worktree.Path] = s
	}
	// wt-a fell back to the identity-only fields from ListWorktrees.
	if byPath[wt1].Worktree.Branch != "feat/a" {
		t.Errorf("wt-a Branch=%q; want feat/a (identity preserved)", byPath[wt1].Worktree.Branch)
	}
	if byPath[wt1].Worktree.DirtyFiles != 0 {
		t.Errorf("wt-a DirtyFiles=%d; want 0 (status failed)", byPath[wt1].Worktree.DirtyFiles)
	}
	// wt-b got the full status.
	if byPath[wt2].Worktree.DirtyFiles != 7 {
		t.Errorf("wt-b DirtyFiles=%d; want 7", byPath[wt2].Worktree.DirtyFiles)
	}
}

// Regression: ListWorktrees populates Main on the primary worktree, but
// WorktreeStatus does not. buildState replaces the worktree state with the
// status result, so Main (like Bare) must be preserved across the merge —
// otherwise the TUI's "cannot prune primary worktree" guard never fires.
func TestSnapshot_PreservesMainAcrossStatusMerge(t *testing.T) {
	now := fixedNow()
	repoRoot := "/repo"
	primary := "/repo"
	secondary := "/repo/.worktrees/feat-a"

	store := openTestSessionStore(t, func(root string) {})

	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: primary, Branch: "main", Main: true},
				{Path: secondary, Branch: "feat/a"},
			},
		},
		statuses: map[string]git.Worktree{
			primary:   {Path: primary, Branch: "main", DirtyFiles: 0, HasUpstream: true},
			secondary: {Path: secondary, Branch: "feat/a", DirtyFiles: 2, HasUpstream: true},
		},
	}
	a := newTestAggregator(t, Config{
		Repos:          []Repo{{Root: repoRoot}},
		SessionStore:   store,
		listWorktrees:  fakes.listWorktrees,
		worktreeStatus: fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
		now:            func() time.Time { return now },
	})
	got, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	byPath := map[string]WorktreeState{}
	for _, s := range got {
		byPath[s.Worktree.Path] = s
	}
	if !byPath[primary].Worktree.Main {
		t.Errorf("primary Worktree.Main = false after status merge; want true (Main must survive like Bare does)")
	}
	if byPath[secondary].Worktree.Main {
		t.Errorf("secondary Worktree.Main = true; want false (only the first ListWorktrees record is primary)")
	}
}

func TestWorktreeStatesEqual_DiffDetection(t *testing.T) {
	base := WorktreeState{
		Repo:     Repo{Root: "/r", Name: "r"},
		Worktree: git.Worktree{Path: "/r/wt", Branch: "main", DirtyFiles: 1},
		PR:       &pr.PR{Number: 1},
		Procs:    []procs.Process{{Pid: 10}, {Pid: 20}},
	}
	clone := base
	clone.Procs = []procs.Process{{Pid: 20}, {Pid: 10}}
	if !worktreeStatesEqual(base, clone) {
		t.Errorf("equal states reported as different (proc ordering should not matter)")
	}

	// UpdatedAt is excluded from equality.
	bumped := base
	bumped.UpdatedAt = time.Now()
	if !worktreeStatesEqual(base, bumped) {
		t.Errorf("UpdatedAt change shouldn't break equality")
	}

	// Dirty changes are detected.
	dirty := base
	dirty.Worktree.DirtyFiles = 5
	if worktreeStatesEqual(base, dirty) {
		t.Errorf("DirtyFiles change should be detected")
	}

	// PR pointer change detected.
	prChanged := base
	pr2 := *base.PR
	pr2.Number = 99
	prChanged.PR = &pr2
	if worktreeStatesEqual(base, prChanged) {
		t.Errorf("PR change should be detected")
	}

	// Different proc pid set detected.
	procsChanged := base
	procsChanged.Procs = []procs.Process{{Pid: 10}, {Pid: 30}}
	if worktreeStatesEqual(base, procsChanged) {
		t.Errorf("Procs pid change should be detected")
	}
}

func TestPathHasPrefix(t *testing.T) {
	cases := []struct {
		cwd, prefix string
		want        bool
	}{
		{"/repo/wt-a", "/repo/wt-a", true},
		{"/repo/wt-a/sub", "/repo/wt-a", true},
		{"/repo/wt-ab", "/repo/wt-a", false},
		{"/repo/wt-ab/sub", "/repo/wt-a", false},
		{"/different", "/repo/wt-a", false},
		{"", "/repo/wt-a", false},
	}
	for _, c := range cases {
		got := pathHasPrefix(c.cwd, c.prefix)
		if got != c.want {
			t.Errorf("pathHasPrefix(%q, %q)=%v; want %v", c.cwd, c.prefix, got, c.want)
		}
	}
}

// installFakePRCache wires a pr.Cache whose underlying gh invocation
// is satisfied by a fake `gh` shell script installed at the head of
// PATH. ttl=24h so the cache never expires during a test. If ghErr
// is non-nil it is simulated by making the fake script exit non-zero
// with the error text on stderr (good enough for ErrNoGH / stale).
//
// `stale=true` means: warm the cache, then flip the fake to fail,
// then Invalidate so the next Get returns stale.
func installFakePRCache(t *testing.T, byRepo map[string][]pr.PR, stale bool, ghErr error) *pr.Cache {
	t.Helper()

	if errors.Is(ghErr, pr.ErrNoGH) {
		// Simulate gh missing: don't put it on PATH at all.
		withEmptyPath(t)
	} else {
		installFakeGH(t, byRepo, ghErr)
	}

	c := pr.NewCache(24 * time.Hour)

	if stale {
		// Warm the cache, then flip to failure mode and invalidate.
		for repoRoot := range byRepo {
			if _, _, err := c.Get(context.Background(), repoRoot); err != nil {
				t.Fatalf("warm cache for %s: %v", repoRoot, err)
			}
		}
		installFakeGH(t, nil, errors.New("network blip"))
		for repoRoot := range byRepo {
			c.Invalidate(repoRoot)
		}
	}
	return c
}

// installFakeGH writes a fake `gh` executable into a tempdir and
// prepends that dir to PATH for the test. The script encodes the
// requested JSON per repo root (the current cwd at invocation time).
//
// PR cache's pr.List passes cwd=repoRoot via cmd.Dir, so $PWD inside
// the script identifies which set of PRs to print.
func installFakeGH(t *testing.T, byRepo map[string][]pr.PR, ghErr error) {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "gh")

	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	if ghErr != nil {
		fmt.Fprintf(&sb, "echo %q 1>&2\nexit 1\n", ghErr.Error())
	} else {
		sb.WriteString(`cwd=$(pwd)` + "\n")
		sb.WriteString(`case "$cwd" in` + "\n")
		for repoRoot, prs := range byRepo {
			fmt.Fprintf(&sb, "  %s)\n", shEscape(repoRoot))
			fmt.Fprintf(&sb, "    cat <<'JSON_EOF'\n%s\nJSON_EOF\n", string(renderGHJSON(prs)))
			sb.WriteString("    ;;\n")
		}
		sb.WriteString("  *) echo '[]' ;;\n")
		sb.WriteString("esac\n")
	}

	if err := os.WriteFile(scriptPath, []byte(sb.String()), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
}

// withEmptyPath strips PATH for the duration of the test so
// exec.LookPath("gh") fails, simulating ErrNoGH.
func withEmptyPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
}

// shEscape returns a single-quoted POSIX shell literal of s, safe to
// drop into a case pattern. Embedded single quotes are not expected
// in repo root fixtures; if they appear they are passed through and
// will trip the script.
func shEscape(s string) string { return "'" + s + "'" }

// TestRefreshAll_CallsProcsOnce ensures the batched procs walk fires
// exactly once per Snapshot, not once per worktree. Regression check
// for the N-walks-per-refresh waste fixed in the macOS port.
func TestRefreshAll_CallsProcsOnce(t *testing.T) {
	const repoRoot = "/repo"
	wt1 := "/repo"
	wt2 := "/repo/.wt/feat-a"
	wt3 := "/repo/.wt/feat-b"

	procsCalls := &atomic.Int64{}
	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: wt1, Branch: "main"},
				{Path: wt2, Branch: "feat-a"},
				{Path: wt3, Branch: "feat-b"},
			},
		},
		procsCalls: procsCalls,
	}

	store := openTestSessionStore(t, func(string) {})

	a := newTestAggregator(t, Config{
		Repos:               []Repo{{Root: repoRoot, Name: "repo"}},
		SessionStore:        store,
		listWorktrees:       fakes.listWorktrees,
		worktreeStatus:      fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
	})

	if _, err := a.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := procsCalls.Load(); got != 1 {
		t.Errorf("listProcsByPrefixes calls = %d, want 1", got)
	}
}

// renderGHJSON serializes a slice of PR into the gh CLI JSON shape
// that pr.List parses. Only the fields List reads are emitted.
func renderGHJSON(prs []pr.PR) []byte {
	var b strings.Builder
	b.WriteString("[")
	for i, p := range prs {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b,
			`{"number":%d,"title":%q,"headRefName":%q,"state":%q,"isDraft":%v,"reviewDecision":%q,"mergedAt":"","updatedAt":"","url":%q,"statusCheckRollup":[]}`,
			p.Number, p.Title, p.HeadBranch, p.State, p.IsDraft, p.ReviewState, p.URL,
		)
	}
	b.WriteString("]")
	return []byte(b.String())
}
