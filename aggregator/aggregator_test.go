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

func (f *fakeSources) listProcs(ctx context.Context, prefix string) ([]procs.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.procsByPrefix != nil {
		return f.procsByPrefix(prefix)
	}
	if f.procsErr != nil {
		return nil, f.procsErr
	}
	return append([]procs.Process(nil), f.procs[prefix]...), nil
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
		listProcs:      fakes.listProcs,
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
