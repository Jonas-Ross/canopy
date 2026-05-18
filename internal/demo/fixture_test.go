package demo_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/internal/demo"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/sessions"
)

func TestBuild_CreatesValidSandbox(t *testing.T) {
	demo.RequireGit(t)

	f, err := demo.Build("")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = f.Cleanup() })

	// Each named worktree directory exists.
	for _, branch := range []string{demo.BranchAuth, demo.BranchDashboard, demo.BranchLogin, demo.BranchDeps} {
		p := f.WorktreePath(branch)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("worktree path missing: %s (%v)", p, err)
		}
	}

	// PR fixture parses as gh JSON.
	prBytes, err := f.PRFixtureBytes()
	if err != nil {
		t.Fatalf("PRFixtureBytes: %v", err)
	}
	if !strings.Contains(string(prBytes), `"headRefName": "feat/auth"`) {
		t.Errorf("PR fixture missing feat/auth entry; got %s", string(prBytes))
	}

	// Sessions index opens.
	store, err := sessions.Open(f.SessionsRoot)
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Live session is attributed to the feat/auth worktree (most-recent cwd
	// match) and the older one to chore/deps.
	authSessions := store.SessionsByCwdPrefix(f.WorktreePath(demo.BranchAuth))
	if len(authSessions) != 1 {
		t.Fatalf("feat/auth session count = %d, want 1", len(authSessions))
	}
	if authSessions[0].Model != "claude-opus-4-7" {
		t.Errorf("feat/auth session model = %q, want claude-opus-4-7", authSessions[0].Model)
	}

	depsSessions := store.SessionsByCwdPrefix(f.WorktreePath(demo.BranchDeps))
	if len(depsSessions) != 1 {
		t.Fatalf("chore/deps session count = %d, want 1", len(depsSessions))
	}
}

func TestBuild_AggregatorSnapshotShowsExpectedState(t *testing.T) {
	demo.RequireGit(t)

	f, err := demo.Build("")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = f.Cleanup() })

	store, err := sessions.Open(f.SessionsRoot)
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Stub gh to return the canned fixture.
	prJSON, err := f.PRFixtureBytes()
	if err != nil {
		t.Fatalf("PRFixtureBytes: %v", err)
	}
	restore := pr.SetRunCmd(func(_ context.Context, _, name string, args ...string) ([]byte, error) {
		if name != "gh" {
			t.Errorf("unexpected exec name = %q, want gh", name)
		}
		return prJSON, nil
	})
	t.Cleanup(func() { pr.SetRunCmd(restore) })

	agg, err := aggregator.New(aggregator.Config{
		Repos:        []aggregator.Repo{{Root: f.RepoRoot, Name: "canopy-demo"}},
		SessionStore: store,
		PRCache:      pr.NewCache(30 * time.Second),
	})
	if err != nil {
		t.Fatalf("aggregator.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	states, err := agg.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	byBranch := map[string]aggregator.WorktreeState{}
	for _, s := range states {
		byBranch[s.Worktree.Branch] = s
	}

	// feat/auth: ahead 1, behind 1, live session, open PR.
	auth, ok := byBranch[demo.BranchAuth]
	if !ok {
		t.Fatalf("snapshot missing feat/auth (have %v)", branchKeys(byBranch))
	}
	if !auth.Worktree.HasUpstream || auth.Worktree.Ahead != 1 || auth.Worktree.Behind != 1 {
		t.Errorf("feat/auth ahead/behind = %d/%d, want 1/1 (HasUpstream=%v)", auth.Worktree.Ahead, auth.Worktree.Behind, auth.Worktree.HasUpstream)
	}
	if auth.Live == nil {
		t.Errorf("feat/auth Live = nil, want a session")
	}
	if auth.PR == nil || auth.PR.Number != 42 {
		t.Errorf("feat/auth PR = %+v, want #42", auth.PR)
	}

	// feat/dashboard: 3 dirty files, no session, draft PR.
	dash, ok := byBranch[demo.BranchDashboard]
	if !ok {
		t.Fatalf("snapshot missing feat/dashboard")
	}
	if dash.Worktree.DirtyFiles != 3 {
		t.Errorf("feat/dashboard DirtyFiles = %d, want 3", dash.Worktree.DirtyFiles)
	}
	if dash.PR == nil || !dash.PR.IsDraft {
		t.Errorf("feat/dashboard PR draft expected, got %+v", dash.PR)
	}

	// chore/deps: ahead 2, behind 1, non-live session, closed PR.
	deps, ok := byBranch[demo.BranchDeps]
	if !ok {
		t.Fatalf("snapshot missing chore/deps")
	}
	if deps.Worktree.Ahead != 2 || deps.Worktree.Behind != 1 {
		t.Errorf("chore/deps ahead/behind = %d/%d, want 2/1", deps.Worktree.Ahead, deps.Worktree.Behind)
	}
	if deps.Live != nil {
		t.Errorf("chore/deps Live should be nil (file mtime is 5m old), got %+v", deps.Live)
	}
	if len(deps.Recent) == 0 {
		t.Errorf("chore/deps Recent should include the 5m-old session, got empty")
	}
}

func branchKeys(m map[string]aggregator.WorktreeState) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
