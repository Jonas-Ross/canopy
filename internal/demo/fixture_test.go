package demo_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/analytics"
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
	if len(authSessions) < 1 {
		t.Fatalf("feat/auth session count = %d, want >= 1", len(authSessions))
	}
	// The live session (11111111-…) uses opus; it should be present.
	var foundLiveAuth bool
	for _, s := range authSessions {
		if s.ID == "11111111-1111-1111-1111-111111111111" {
			foundLiveAuth = true
			if s.Model != "claude-opus-4-7" {
				t.Errorf("feat/auth live session model = %q, want claude-opus-4-7", s.Model)
			}
			break
		}
	}
	if !foundLiveAuth {
		t.Errorf("feat/auth live session (11111111-…) missing from store")
	}

	depsSessions := store.SessionsByCwdPrefix(f.WorktreePath(demo.BranchDeps))
	if len(depsSessions) < 1 {
		t.Fatalf("chore/deps session count = %d, want >= 1", len(depsSessions))
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

// TestBuild_HistoricalSessionsForForensics verifies that the fixture seeds
// enough historical sessions for the forensics tab to render meaningful data.
func TestBuild_HistoricalSessionsForForensics(t *testing.T) {
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

	now := time.Now()
	snap, err := analytics.Build(store, now)
	if err != nil {
		t.Fatalf("analytics.Build: %v", err)
	}

	// At least 12 sessions in the snapshot.
	if len(snap.Sessions) < 12 {
		t.Errorf("Sessions count = %d, want >= 12", len(snap.Sessions))
	}

	// Span at least 8 distinct UTC days within the last 30.
	distinctDays := make(map[string]struct{})
	for _, s := range snap.Sessions {
		day := s.UpdatedAt.UTC().Format("2006-01-02")
		distinctDays[day] = struct{}{}
	}
	if len(distinctDays) < 8 {
		t.Errorf("distinct days = %d, want >= 8; days: %v", len(distinctDays), distinctDays)
	}

	// At least 2 distinct models.
	distinctModels := make(map[string]struct{})
	for _, s := range snap.Sessions {
		if s.Model != "" {
			distinctModels[s.Model] = struct{}{}
		}
	}
	if len(distinctModels) < 2 {
		t.Errorf("distinct models = %d, want >= 2; models: %v", len(distinctModels), distinctModels)
	}

	// At least 5 tool types used across all sessions.
	if len(snap.Tools) < 5 {
		t.Errorf("distinct tools = %d, want >= 5; tools: %v", len(snap.Tools), snap.Tools)
	}

	// The 2 original sessions are still present.
	foundOpus := false
	foundSonnet := false
	for _, s := range snap.Sessions {
		if s.ID == "11111111-1111-1111-1111-111111111111" {
			foundOpus = true
		}
		if s.ID == "22222222-2222-2222-2222-222222222222" {
			foundSonnet = true
		}
	}
	if !foundOpus {
		t.Errorf("original opus session (11111111-…) not found in snapshot")
	}
	if !foundSonnet {
		t.Errorf("original sonnet session (22222222-…) not found in snapshot")
	}
}
