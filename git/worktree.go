// Package git enumerates and inspects git worktrees by shelling out
// to the git binary. ListWorktrees is cheap (one invocation, identity
// only); WorktreeStatus is heavier (four invocations, full state).
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Worktree is the per-worktree state surfaced by this package.
//
// ListWorktrees populates the cheap-to-read identification fields
// (Path, Branch, Bare, Detached). The status fields (DirtyFiles, Ahead,
// Behind, HasUpstream, LastCommit) are populated only by WorktreeStatus,
// which runs the heavier per-worktree git invocations.
type Worktree struct {
	Path        string // absolute path
	Branch      string // current branch, empty if detached
	Bare        bool
	Detached    bool
	DirtyFiles  int    // 0 = clean
	Ahead       int    // commits ahead of upstream
	Behind      int    // commits behind upstream
	HasUpstream bool   // false if no @{u}; in that case Ahead/Behind are 0 and meaningless
	LastCommit  Commit
}

// Commit is the minimal commit metadata used in worktree summaries.
type Commit struct {
	Hash    string    // short sha
	Subject string
	Author  string
	When    time.Time
}

// runCmd is the seam every git invocation goes through. Tests replace
// it to return fixture bytes.
var runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ListWorktrees enumerates all worktrees of the repo at repoRoot. It
// runs a single `git worktree list --porcelain` and parses the result;
// status detail (DirtyFiles, Ahead/Behind, LastCommit) is left zero.
// Callers needing detail must invoke WorktreeStatus for each path.
func ListWorktrees(ctx context.Context, repoRoot string) ([]Worktree, error) {
	out, err := runCmd(ctx, "git", "-C", repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git: list worktrees: %w", err)
	}
	return parseWorktreeList(out), nil
}

// WorktreeStatus returns the fully populated state of the worktree at
// path. Three git invocations: status, rev-list, log. A missing upstream
// on rev-list is not an error — HasUpstream is set to false and Ahead /
// Behind are left at zero.
func WorktreeStatus(ctx context.Context, path string) (Worktree, error) {
	wt := Worktree{Path: path}

	branch, detached, err := readHeadBranch(ctx, path)
	if err != nil {
		return Worktree{}, fmt.Errorf("git: head: %w", err)
	}
	wt.Branch = branch
	wt.Detached = detached

	dirty, err := readDirtyCount(ctx, path)
	if err != nil {
		return Worktree{}, fmt.Errorf("git: status: %w", err)
	}
	wt.DirtyFiles = dirty

	ahead, behind, hasUpstream, err := readAheadBehind(ctx, path)
	if err != nil {
		return Worktree{}, fmt.Errorf("git: rev-list: %w", err)
	}
	wt.Ahead = ahead
	wt.Behind = behind
	wt.HasUpstream = hasUpstream

	commit, err := readLastCommit(ctx, path)
	if err != nil {
		return Worktree{}, fmt.Errorf("git: log: %w", err)
	}
	wt.LastCommit = commit

	return wt, nil
}

// readHeadBranch returns the current branch, or "" with detached=true
// when HEAD is detached. symbolic-ref exits non-zero on detach; that
// is the normal state, not an error.
func readHeadBranch(ctx context.Context, path string) (string, bool, error) {
	out, err := runCmd(ctx, "git", "-C", path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		if isDetachedHeadErr(err) {
			return "", true, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(string(out)), false, nil
}

// readDirtyCount runs `git status --porcelain=v1` and returns the count
// of non-empty lines. v1 is pinned because v2 reordered columns; we
// want stable output across git versions.
func readDirtyCount(ctx context.Context, path string) (int, error) {
	out, err := runCmd(ctx, "git", "-C", path, "status", "--porcelain=v1")
	if err != nil {
		return 0, err
	}
	out = bytes.TrimRight(out, "\r\n")
	if len(out) == 0 {
		return 0, nil
	}
	return bytes.Count(out, []byte("\n")) + 1, nil
}

// readAheadBehind runs `git rev-list --left-right --count @{u}...HEAD`.
// The command prints two numbers separated by a tab: "<behind>\t<ahead>"
// (left = upstream-only, right = HEAD-only). When the worktree has no
// upstream configured, rev-list exits non-zero with "no upstream
// configured for branch" on stderr; that case yields hasUpstream=false
// with no propagated error.
func readAheadBehind(ctx context.Context, path string) (ahead, behind int, hasUpstream bool, err error) {
	out, runErr := runCmd(ctx, "git", "-C", path, "rev-list", "--left-right", "--count", "@{u}...HEAD")
	if runErr != nil {
		if isNoUpstreamErr(runErr) {
			return 0, 0, false, nil
		}
		return 0, 0, false, runErr
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return 0, 0, false, fmt.Errorf("rev-list: unexpected output %q", string(out))
	}
	behindN, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, false, fmt.Errorf("rev-list: parse behind %q: %w", fields[0], err)
	}
	aheadN, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false, fmt.Errorf("rev-list: parse ahead %q: %w", fields[1], err)
	}
	return aheadN, behindN, true, nil
}

// readLastCommit runs `git log -1 --format=%h%x00%s%x00%an%x00%cI` and
// parses the NUL-separated record. NUL is the only safe separator: a
// commit subject can contain any printable character including tabs.
func readLastCommit(ctx context.Context, path string) (Commit, error) {
	out, err := runCmd(ctx, "git", "-C", path, "log", "-1", "--format=%h%x00%s%x00%an%x00%cI")
	if err != nil {
		return Commit{}, err
	}
	// `git log` terminates the record with a trailing newline.
	trimmed := bytes.TrimRight(out, "\n")
	if len(trimmed) == 0 {
		// No commits in the repo yet. Treat as empty Commit, no error;
		// the worktree exists but has nothing to summarize.
		return Commit{}, nil
	}
	parts := bytes.Split(trimmed, []byte{0})
	if len(parts) != 4 {
		return Commit{}, fmt.Errorf("log: unexpected field count %d in %q", len(parts), string(trimmed))
	}
	when, err := time.Parse(time.RFC3339, string(parts[3]))
	if err != nil {
		return Commit{}, fmt.Errorf("log: parse timestamp %q: %w", string(parts[3]), err)
	}
	return Commit{
		Hash:    string(parts[0]),
		Subject: string(parts[1]),
		Author:  string(parts[2]),
		When:    when.UTC(),
	}, nil
}

// parseWorktreeList parses `git worktree list --porcelain` output.
//
// Format: records separated by a blank line. Each record carries at
// least a `worktree <path>` line; optional lines are `HEAD <sha>`,
// `branch refs/heads/<name>`, plus single-token markers `bare` and
// `detached`. The main worktree may be bare and have no HEAD/branch.
func parseWorktreeList(out []byte) []Worktree {
	var result []Worktree
	var cur Worktree
	inRecord := false

	flush := func() {
		if inRecord && cur.Path != "" {
			result = append(result, cur)
		}
		cur = Worktree{}
		inRecord = false
	}

	for _, raw := range bytes.Split(out, []byte("\n")) {
		line := string(bytes.TrimRight(raw, "\r"))
		if line == "" {
			flush()
			continue
		}
		inRecord = true
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Detached = true
			// HEAD and other lines are ignored; the surface we expose
			// does not need them.
		}
	}
	flush()
	return result
}

// isDetachedHeadErr reports whether err looks like the symbolic-ref
// failure for a detached HEAD. The check inspects stderr captured on
// *exec.ExitError; tests that stub runCmd can return any error with the
// signal phrase in its message and the logic still applies.
func isDetachedHeadErr(err error) bool {
	return errorContains(err, "ref HEAD is not a symbolic ref") ||
		errorContains(err, "not a symbolic ref")
}

// isNoUpstreamErr reports whether err is the rev-list failure for a
// branch without an upstream configured. Same fallback as
// isDetachedHeadErr: tests can return a plain error carrying the phrase.
func isNoUpstreamErr(err error) bool {
	return errorContains(err, "no upstream configured") ||
		errorContains(err, "unknown revision or path not in the working tree")
}

// errorContains looks for substr in both err.Error() and, when present,
// the Stderr captured on *exec.ExitError. Real git invocations report
// the descriptive text on stderr while leaving err.Error() as a bare
// "exit status N"; fixture-based tests typically embed the phrase in
// err.Error() directly.
func errorContains(err error, substr string) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), substr) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		if strings.Contains(string(exitErr.Stderr), substr) {
			return true
		}
	}
	return false
}
