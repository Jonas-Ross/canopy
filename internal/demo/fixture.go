// Package demo builds a throwaway "test repository" environment for
// validating the canopy TUI. Used by the `canopy demo` subcommand and by
// internal goldens/scripts. Production code paths never reach this package.
package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonasross/canopy/tui"
)

const fixturePrefix = "canopy-demo-"

// Branch names used across the fixture. Stable so goldens and replay
// scripts can reference them by name.
const (
	BranchMain      = "main"
	BranchAuth      = "feat/auth"
	BranchDashboard = "feat/dashboard"
	BranchLogin     = "fix/login"
	BranchDeps      = "chore/deps"
)

// Fixture is the on-disk demo layout produced by Build.
type Fixture struct {
	Root          string // tmpdir root (symlinks resolved)
	RepoRoot      string // <Root>/repo — main worktree
	SessionsRoot  string // <Root>/claude/projects
	PRFixturePath string // <Root>/pr-fixture.json
}

// Build assembles the fixture under parent. parent="" calls os.MkdirTemp.
func Build(parent string) (*Fixture, error) {
	root, err := mkRoot(parent)
	if err != nil {
		return nil, fmt.Errorf("demo: mkdir root: %w", err)
	}
	f := &Fixture{
		Root:          root,
		RepoRoot:      filepath.Join(root, "repo"),
		SessionsRoot:  filepath.Join(root, "claude", "projects"),
		PRFixturePath: filepath.Join(root, "pr-fixture.json"),
	}
	if err := os.MkdirAll(f.SessionsRoot, 0o755); err != nil {
		return nil, err
	}
	if err := f.initRepo(); err != nil {
		return nil, fmt.Errorf("demo: init repo: %w", err)
	}
	if err := f.buildWorktrees(); err != nil {
		return nil, fmt.Errorf("demo: build worktrees: %w", err)
	}
	if err := f.writeSessions(); err != nil {
		return nil, fmt.Errorf("demo: write sessions: %w", err)
	}
	if err := f.writePRFixture(); err != nil {
		return nil, fmt.Errorf("demo: write PR fixture: %w", err)
	}
	return f, nil
}

// Cleanup removes the tmpdir tree.
func (f *Fixture) Cleanup() error {
	return os.RemoveAll(f.Root)
}

// PRFixtureBytes returns the canned `gh pr list --json` output bytes.
func (f *Fixture) PRFixtureBytes() ([]byte, error) {
	return os.ReadFile(f.PRFixturePath)
}

// WorktreePath returns the absolute filesystem path of a fixture worktree.
// Defers to tui.WorktreePath so the fixture stays in lockstep with where
// the real `n` (new worktree) flow places branches.
func (f *Fixture) WorktreePath(branch string) string {
	if branch == BranchMain {
		return f.RepoRoot
	}
	return tui.WorktreePath(f.RepoRoot, branch)
}

// RequireGit skips t when git is not on PATH. Exported so other packages
// can gate fixture-using tests on the same precondition.
func RequireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping fixture-backed test")
	}
}


func mkRoot(parent string) (string, error) {
	var dir string
	var err error
	if parent == "" {
		dir, err = os.MkdirTemp("", fixturePrefix+"*")
	} else {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
		dir, err = os.MkdirTemp(parent, fixturePrefix+"*")
	}
	if err != nil {
		return "", err
	}
	// Resolve symlinks so paths match what `git worktree list` will
	// report. On macOS /var → /private/var, and a mismatch breaks the
	// longest-prefix attribution in aggregator.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved, nil
	}
	return dir, nil
}

func (f *Fixture) run(args ...string) error {
	return f.runIn(f.RepoRoot, args...)
}

func (f *Fixture) runIn(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	// Isolate from the user's global git config so signing keys, hooks,
	// or identity overrides don't interfere with the throwaway repo.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Canopy Demo",
		"GIT_AUTHOR_EMAIL=demo@canopy.test",
		"GIT_COMMITTER_NAME=Canopy Demo",
		"GIT_COMMITTER_EMAIL=demo@canopy.test",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", args[0], strings.Join(args[1:], " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (f *Fixture) initRepo() error {
	if err := os.MkdirAll(f.RepoRoot, 0o755); err != nil {
		return err
	}
	if err := f.run("git", "init", "-q", "-b", "main", "."); err != nil {
		return err
	}
	files := map[string]string{
		"README.md":  "# canopy demo\n",
		".gitignore": ".worktrees/\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(f.RepoRoot, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := f.run("git", "add", "README.md", ".gitignore"); err != nil {
		return err
	}
	return f.run("git", "commit", "-q", "-m", "init")
}

type worktreeSpec struct {
	Branch       string
	DirtyFiles   int
	AheadCommits int
	HasUpstream  bool
}

func (f *Fixture) buildWorktrees() error {
	specs := []worktreeSpec{
		{Branch: BranchAuth, AheadCommits: 1, HasUpstream: true},
		{Branch: BranchDashboard, DirtyFiles: 3, HasUpstream: true},
		{Branch: BranchLogin, HasUpstream: true},
		{Branch: BranchDeps, AheadCommits: 2, HasUpstream: true},
	}
	for _, s := range specs {
		path := f.WorktreePath(s.Branch)
		if err := f.run("git", "worktree", "add", "-q", "-b", s.Branch, path); err != nil {
			return err
		}
		if s.HasUpstream {
			if err := f.runIn(path, "git", "branch", "-q", "--set-upstream-to=main", s.Branch); err != nil {
				return err
			}
		}
		for i := 0; i < s.AheadCommits; i++ {
			msg := fmt.Sprintf("%s commit %d", s.Branch, i+1)
			if err := f.runIn(path, "git", "commit", "-q", "--allow-empty", "-m", msg); err != nil {
				return err
			}
		}
		for i := 0; i < s.DirtyFiles; i++ {
			p := filepath.Join(path, fmt.Sprintf("dirty-%d.txt", i+1))
			if err := os.WriteFile(p, []byte("wip\n"), 0o644); err != nil {
				return err
			}
		}
	}
	// Advance main by one commit so every branch shows behind=1.
	return f.run("git", "commit", "-q", "--allow-empty", "-m", "main advance")
}

func (f *Fixture) writeSessions() error {
	now := time.Now()
	if err := f.writeSession("canopy-demo-feat-auth", "11111111-1111-1111-1111-111111111111", f.WorktreePath(BranchAuth), "claude-opus-4-7", now); err != nil {
		return err
	}
	// 5 minutes ago — past LiveWindow (120s) so the session appears in
	// Recent but not Live.
	older := now.Add(-5 * time.Minute)
	return f.writeSession("canopy-demo-chore-deps", "22222222-2222-2222-2222-222222222222", f.WorktreePath(BranchDeps), "claude-sonnet-4-6", older)
}

func (f *Fixture) writeSession(projectDir, sessionID, cwd, model string, mtime time.Time) error {
	dir := filepath.Join(f.SessionsRoot, projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	baseTS := mtime.Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)
	laterTS := mtime.UTC().Format(time.RFC3339Nano)
	lines := []map[string]any{
		{"type": "attachment", "uuid": "a-001", "sessionId": sessionID, "timestamp": baseTS, "cwd": cwd, "subtype": "system_info"},
		{"type": "user", "uuid": "u-001", "sessionId": sessionID, "timestamp": baseTS, "cwd": cwd,
			"message": map[string]any{"role": "user", "content": "demo prompt"}},
		{"type": "assistant", "uuid": "a-002", "sessionId": sessionID, "timestamp": laterTS, "cwd": cwd,
			"message": map[string]any{
				"id": "msg_01", "role": "assistant", "model": model,
				"content":     []any{map[string]any{"type": "text", "text": "demo response"}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
			}},
	}
	fp, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(fp)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			fp.Close()
			return err
		}
	}
	if err := fp.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, mtime, mtime)
}

func (f *Fixture) writePRFixture() error {
	now := time.Now().UTC().Format(time.RFC3339)
	payload := []map[string]any{
		{
			"number": 42, "title": "auth: bcrypt migration",
			"headRefName":       BranchAuth,
			"state":             "OPEN",
			"isDraft":           false,
			"statusCheckRollup": []map[string]any{{"status": "COMPLETED", "conclusion": "SUCCESS"}},
			"reviewDecision":    "REVIEW_REQUIRED",
			"mergedAt":          "",
			"updatedAt":         now,
			"url":               "https://example.invalid/canopy-demo/pull/42",
		},
		{
			"number": 43, "title": "dashboard: shipping screens",
			"headRefName":       BranchDashboard,
			"state":             "OPEN",
			"isDraft":           true,
			"statusCheckRollup": []map[string]any{{"status": "IN_PROGRESS", "conclusion": ""}},
			"reviewDecision":    "",
			"mergedAt":          "",
			"updatedAt":         now,
			"url":               "https://example.invalid/canopy-demo/pull/43",
		},
		{
			"number": 41, "title": "fix: login redirect",
			"headRefName":       BranchLogin,
			"state":             "MERGED",
			"isDraft":           false,
			"statusCheckRollup": []map[string]any{{"status": "COMPLETED", "conclusion": "SUCCESS"}},
			"reviewDecision":    "APPROVED",
			"mergedAt":          now,
			"updatedAt":         now,
			"url":               "https://example.invalid/canopy-demo/pull/41",
		},
		{
			"number": 40, "title": "chore: bump deps",
			"headRefName":       BranchDeps,
			"state":             "CLOSED",
			"isDraft":           false,
			"statusCheckRollup": []map[string]any{{"status": "COMPLETED", "conclusion": "FAILURE"}},
			"reviewDecision":    "CHANGES_REQUESTED",
			"mergedAt":          "",
			"updatedAt":         now,
			"url":               "https://example.invalid/canopy-demo/pull/40",
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.PRFixturePath, data, 0o644)
}
