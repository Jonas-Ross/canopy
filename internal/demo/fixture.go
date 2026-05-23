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
	if err := f.writeSession("canopy-demo-chore-deps", "22222222-2222-2222-2222-222222222222", f.WorktreePath(BranchDeps), "claude-sonnet-4-6", older); err != nil {
		return err
	}
	return f.writeHistoricalSessions(now)
}

// historicalSession describes one synthetic past session for the forensics tab.
type historicalSession struct {
	projectDir string
	id         string
	cwd        string
	model      string
	daysAgo    int // mtime = now - daysAgo*24h
	tools      map[string]int
	input      int
	output     int
	cacheRead  int
	cacheCrea  int
}

// writeHistoricalSessions seeds ~13 deterministic sessions spread across the
// last 30 days. IDs use the f0000001-… scheme so they are stable across runs.
// The existing two live/recent sessions (11111111-… and 22222222-…) are NOT
// touched here.
func (f *Fixture) writeHistoricalSessions(now time.Time) error {
	specs := []historicalSession{
		// Day -1: two feat/auth opus sessions with varied tool mixes.
		{
			projectDir: "canopy-hist-auth-01",
			id:         "f0000001-0000-0000-0000-000000000001",
			cwd:        f.WorktreePath(BranchAuth),
			model:      "claude-opus-4-7",
			daysAgo:    1,
			tools:      map[string]int{"Bash": 4, "Read": 6, "Edit": 3},
			input:      45000, output: 8000, cacheRead: 30000, cacheCrea: 3000,
		},
		{
			projectDir: "canopy-hist-auth-02",
			id:         "f0000002-0000-0000-0000-000000000002",
			cwd:        f.WorktreePath(BranchAuth),
			model:      "claude-opus-4-7",
			daysAgo:    1,
			tools:      map[string]int{"Read": 8, "Grep": 5, "Write": 2},
			input:      32000, output: 6500, cacheRead: 20000, cacheCrea: 2500,
		},
		// Day -2: feat/dashboard sonnet.
		{
			projectDir: "canopy-hist-dashboard-01",
			id:         "f0000003-0000-0000-0000-000000000003",
			cwd:        f.WorktreePath(BranchDashboard),
			model:      "claude-sonnet-4-6",
			daysAgo:    2,
			tools:      map[string]int{"Read": 5, "Edit": 4, "Bash": 2},
			input:      28000, output: 7000, cacheRead: 15000, cacheCrea: 2000,
		},
		// Day -3: feat/auth opus, heavy Bash usage.
		{
			projectDir: "canopy-hist-auth-03",
			id:         "f0000004-0000-0000-0000-000000000004",
			cwd:        f.WorktreePath(BranchAuth),
			model:      "claude-opus-4-7",
			daysAgo:    3,
			tools:      map[string]int{"Bash": 12, "Read": 4, "Grep": 3},
			input:      70000, output: 14000, cacheRead: 90000, cacheCrea: 4500,
		},
		// Day -4: fix/login opus.
		{
			projectDir: "canopy-hist-login-01",
			id:         "f0000005-0000-0000-0000-000000000005",
			cwd:        f.WorktreePath(BranchLogin),
			model:      "claude-opus-4-7",
			daysAgo:    4,
			tools:      map[string]int{"Read": 6, "Edit": 5, "Bash": 3},
			input:      40000, output: 9000, cacheRead: 25000, cacheCrea: 3200,
		},
		// Day -5: feat/auth opus.
		{
			projectDir: "canopy-hist-auth-04",
			id:         "f0000006-0000-0000-0000-000000000006",
			cwd:        f.WorktreePath(BranchAuth),
			model:      "claude-opus-4-7",
			daysAgo:    5,
			tools:      map[string]int{"Bash": 7, "Write": 4, "Read": 5},
			input:      55000, output: 11000, cacheRead: 60000, cacheCrea: 4000,
		},
		// Day -6: feat/dashboard sonnet.
		{
			projectDir: "canopy-hist-dashboard-02",
			id:         "f0000007-0000-0000-0000-000000000007",
			cwd:        f.WorktreePath(BranchDashboard),
			model:      "claude-sonnet-4-6",
			daysAgo:    6,
			tools:      map[string]int{"Edit": 6, "Read": 4, "Grep": 2},
			input:      22000, output: 5500, cacheRead: 12000, cacheCrea: 1800,
		},
		// Day -7: chore/deps sonnet.
		{
			projectDir: "canopy-hist-deps-01",
			id:         "f0000008-0000-0000-0000-000000000008",
			cwd:        f.WorktreePath(BranchDeps),
			model:      "claude-sonnet-4-6",
			daysAgo:    7,
			tools:      map[string]int{"Bash": 3, "Read": 3, "Edit": 2},
			input:      18000, output: 4500, cacheRead: 10000, cacheCrea: 1500,
		},
		// Day -8: fix/login opus.
		{
			projectDir: "canopy-hist-login-02",
			id:         "f0000009-0000-0000-0000-000000000009",
			cwd:        f.WorktreePath(BranchLogin),
			model:      "claude-opus-4-7",
			daysAgo:    8,
			tools:      map[string]int{"Read": 7, "Edit": 3, "Bash": 2},
			input:      38000, output: 8500, cacheRead: 22000, cacheCrea: 2800,
		},
		// Day -10: feat/auth opus.
		{
			projectDir: "canopy-hist-auth-05",
			id:         "f0000010-0000-0000-0000-000000000010",
			cwd:        f.WorktreePath(BranchAuth),
			model:      "claude-opus-4-7",
			daysAgo:    10,
			tools:      map[string]int{"Bash": 5, "Read": 8, "Write": 3},
			input:      62000, output: 12000, cacheRead: 75000, cacheCrea: 4200,
		},
		// Day -12: chore/deps sonnet.
		{
			projectDir: "canopy-hist-deps-02",
			id:         "f0000011-0000-0000-0000-000000000011",
			cwd:        f.WorktreePath(BranchDeps),
			model:      "claude-sonnet-4-6",
			daysAgo:    12,
			tools:      map[string]int{"Read": 4, "Edit": 4, "Bash": 3},
			input:      25000, output: 6000, cacheRead: 14000, cacheCrea: 2000,
		},
		// Day -15: feat/dashboard sonnet.
		{
			projectDir: "canopy-hist-dashboard-03",
			id:         "f0000012-0000-0000-0000-000000000012",
			cwd:        f.WorktreePath(BranchDashboard),
			model:      "claude-sonnet-4-6",
			daysAgo:    15,
			tools:      map[string]int{"Edit": 5, "Read": 6, "Grep": 4},
			input:      30000, output: 7500, cacheRead: 18000, cacheCrea: 2300,
		},
		// Day -20: feat/auth opus.
		{
			projectDir: "canopy-hist-auth-06",
			id:         "f0000013-0000-0000-0000-000000000013",
			cwd:        f.WorktreePath(BranchAuth),
			model:      "claude-opus-4-7",
			daysAgo:    20,
			tools:      map[string]int{"Bash": 9, "Read": 5, "Write": 2, "Edit": 4},
			input:      80000, output: 15000, cacheRead: 100000, cacheCrea: 5000,
		},
		// Day -25: main opus — one session on main.
		{
			projectDir: "canopy-hist-main-01",
			id:         "f0000014-0000-0000-0000-000000000014",
			cwd:        f.WorktreePath(BranchMain),
			model:      "claude-opus-4-7",
			daysAgo:    25,
			tools:      map[string]int{"Read": 3, "Bash": 2, "Grep": 4},
			input:      20000, output: 5000, cacheRead: 11000, cacheCrea: 1600,
		},
		// Day -2: feat/dashboard sonnet with web research traffic — exercises
		// the web (WebFetch/WebSearch) category in the tools view.
		{
			projectDir: "canopy-hist-dashboard-04",
			id:         "f0000015-0000-0000-0000-000000000015",
			cwd:        f.WorktreePath(BranchDashboard),
			model:      "claude-sonnet-4-6",
			daysAgo:    2,
			tools:      map[string]int{"WebFetch": 8, "WebSearch": 7, "Read": 4, "Edit": 2},
			input:      26000, output: 6500, cacheRead: 14000, cacheCrea: 1900,
		},
		// Day -3: feat/auth opus session that drives an MCP server — exercises
		// the mcp category and the multi-segment-server simplification path.
		{
			projectDir: "canopy-hist-auth-07",
			id:         "f0000016-0000-0000-0000-000000000016",
			cwd:        f.WorktreePath(BranchAuth),
			model:      "claude-opus-4-7",
			daysAgo:    3,
			tools: map[string]int{
				"mcp__plugin_github_github__list_issues":         16,
				"mcp__plugin_github_github__create_pull_request": 8,
				"Read": 5, "Bash": 4,
			},
			input: 38000, output: 8000, cacheRead: 22000, cacheCrea: 2700,
		},
		// Day -4: chore/deps opus session that dispatches subagents and uses
		// SendMessage — exercises the task category.
		{
			projectDir: "canopy-hist-deps-03",
			id:         "f0000017-0000-0000-0000-000000000017",
			cwd:        f.WorktreePath(BranchDeps),
			model:      "claude-opus-4-7",
			daysAgo:    4,
			tools:      map[string]int{"SendMessage": 18, "Task": 16, "TaskUpdate": 10, "Read": 4, "Bash": 2},
			input:      33000, output: 7200, cacheRead: 18000, cacheCrea: 2300,
		},
	}
	for _, spec := range specs {
		mtime := now.Add(-time.Duration(spec.daysAgo) * 24 * time.Hour)
		if err := f.writeSessionFull(spec.projectDir, spec.id, spec.cwd, spec.model, mtime, spec.tools, spec.input, spec.output, spec.cacheRead, spec.cacheCrea); err != nil {
			return err
		}
	}
	return nil
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

// writeSessionFull writes a richer JSONL session with tool_use blocks and
// realistic token counts. All tool_use blocks are bundled under a single
// assistant message ID so token dedup math is correct.
func (f *Fixture) writeSessionFull(projectDir, sessionID, cwd, model string, mtime time.Time, tools map[string]int, inputTokens, outputTokens, cacheRead, cacheCrea int) error {
	dir := filepath.Join(f.SessionsRoot, projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, sessionID+".jsonl")

	baseTS := mtime.Add(-30 * time.Minute).UTC().Format(time.RFC3339Nano)
	laterTS := mtime.UTC().Format(time.RFC3339Nano)

	// Build content slice: text block + one tool_use block per invocation.
	content := []any{
		map[string]any{"type": "text", "text": "demo historical response"},
	}
	toolIdx := 0
	for toolName, count := range tools {
		for i := 0; i < count; i++ {
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    fmt.Sprintf("tu_%s_%d_%d", sessionID[:8], toolIdx, i),
				"name":  toolName,
				"input": map[string]any{},
			})
			toolIdx++
		}
	}
	stopReason := "end_turn"
	if len(tools) > 0 {
		stopReason = "tool_use"
	}

	lines := []map[string]any{
		{
			"type": "attachment", "uuid": sessionID + "-sys",
			"sessionId": sessionID, "timestamp": baseTS, "cwd": cwd, "subtype": "system_info",
		},
		{
			"type": "user", "uuid": sessionID + "-u0",
			"sessionId": sessionID, "timestamp": baseTS, "cwd": cwd,
			"message": map[string]any{"role": "user", "content": "demo historical prompt"},
		},
		{
			"type": "assistant", "uuid": sessionID + "-a0",
			"sessionId": sessionID, "timestamp": laterTS, "cwd": cwd,
			"message": map[string]any{
				"id":          sessionID + "-msg0",
				"role":        "assistant",
				"model":       model,
				"content":     content,
				"stop_reason": stopReason,
				"usage": map[string]any{
					"input_tokens":                inputTokens,
					"output_tokens":               outputTokens,
					"cache_read_input_tokens":     cacheRead,
					"cache_creation_input_tokens": cacheCrea,
				},
			},
		},
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
