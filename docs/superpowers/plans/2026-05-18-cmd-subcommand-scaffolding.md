# Cobra Subcommand Scaffolding (M5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add empty cobra subcommand stubs (`canopy worktree …`, `canopy sessions …`, `canopy prune`) so the CLI surface can grow into real commands later without restructuring the root.

**Architecture:** One source file per top-level subcommand under `cmd/`, each registering its parent + leaves on `rootCmd` via `init()`. Leaves share a tiny `stubRunE(name)` helper that prints `canopy <name>: not yet implemented (M5 placeholder)` to stderr and returns nil. Parents have no `RunE`, so cobra prints help when invoked bare. Tests mirror the existing `cmd/root_test.go` pattern: drive `rootCmd` with `SetArgs` and assert the captured output.

**Tech Stack:** Go 1.24, `github.com/spf13/cobra`, stdlib `bytes` + `strings` + `testing`.

**Branch:** `feat/cmd-subcommand-scaffolding` off `main`.

**Issue:** [#6 — M5: cobra subcommand scaffolding](https://github.com/jonasross/canopy/issues/6)

---

## File Structure

- **Create** `cmd/stub.go` — exports `stubRunE(name string) func(*cobra.Command, []string) error`. The only function in the file. Lives separately so it's easy to grep and easy to delete once every command has a real implementation.
- **Create** `cmd/worktree.go` — defines `worktreeCmd` (parent, no `RunE`) and three leaf commands `worktreeListCmd`, `worktreeNewCmd`, `worktreePruneCmd`; `init()` wires `worktree` under `rootCmd` and the leaves under `worktreeCmd`.
- **Create** `cmd/worktree_test.go` — one test per leaf + one test asserting `canopy worktree --help` lists the three leaves.
- **Create** `cmd/sessions.go` — defines `sessionsCmd` (parent, no `RunE`) and two leaves `sessionsListCmd`, `sessionsTailCmd`; `init()` wires similarly.
- **Create** `cmd/sessions_test.go` — one test per leaf + one parent-help test.
- **Create** `cmd/prune.go` — defines `pruneCmd` as a standalone leaf using `stubRunE`. `init()` wires it directly under `rootCmd`. Documents in its `Long` that it will eventually call the `worktree prune` path.
- **Create** `cmd/prune_test.go` — single stub test.
- **Modify** `cmd/root_test.go` — append a `TestRootCommand_Help_ListsSubcommands` test that asserts the three new top-level commands appear in `--help` output.

The existing `cmd/root.go`, `cmd/demo.go`, `cmd/demo_script.go`, and their tests are untouched.

---

## Conventions to follow

- **Stub message format (exact):** `canopy <name>: not yet implemented (M5 placeholder)` followed by a newline, written to `cmd.ErrOrStderr()`. The `<name>` is the space-joined command path, e.g. `worktree list`, `sessions tail`, `prune`.
- **Help fields:** Every command (parent and leaf) must populate `Use`, `Short`, and `Long`. Leaves use a single-sentence `Short` and a 1–3 line `Long`.
- **Parents have no `RunE`.** Cobra prints help automatically; tests assert this.
- **Test capture pattern** (matches `cmd/root_test.go:16-29`):
  ```go
  var out bytes.Buffer
  rootCmd.SetOut(&out)
  rootCmd.SetErr(&out)
  rootCmd.SetArgs([]string{"worktree", "list"})
  if err := rootCmd.Execute(); err != nil {
      t.Fatalf("execute: %v", err)
  }
  got := out.String()
  if !strings.Contains(got, "canopy worktree list: not yet implemented (M5 placeholder)") {
      t.Errorf("stub message missing; got %q", got)
  }
  ```
  Because `rootCmd` is a package-level global, every test must call `SetArgs` itself — leftover args from a previous test must not be relied on.
- **Commit style** (per `CLAUDE.md`): Conventional Commits with `cmd` scope; imperative mood, lowercase subject, no trailing period.

---

## Task 0: Skeleton helper

**Files:**
- Create: `cmd/stub.go`

- [ ] **Step 1: Confirm you're on `feat/cmd-subcommand-scaffolding`**

Work is performed inside the pre-created worktree on branch `feat/cmd-subcommand-scaffolding`. Verify before writing code:

```bash
git branch --show-current
```

Expected output: `feat/cmd-subcommand-scaffolding`. If this prints anything else, stop and escalate — do not switch branches or create a new one.

- [ ] **Step 2: Write `cmd/stub.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// stubRunE returns a cobra RunE that prints a uniform M5 placeholder line
// to stderr and exits cleanly. name should be the space-joined command path
// (e.g. "worktree list", "prune"). The helper exists so the placeholder text
// stays uniform across the six leaves added in M5 and can be grepped &
// removed wholesale as real implementations land.
func stubRunE(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		fmt.Fprintf(cmd.ErrOrStderr(), "canopy %s: not yet implemented (M5 placeholder)\n", name)
		return nil
	}
}
```

- [ ] **Step 3: Confirm the package still builds**

Run: `go build ./cmd/...`
Expected: no output, exit 0. (No test yet — `stubRunE` is exercised through the leaf-command tests in later tasks.)

- [ ] **Step 4: Commit**

```bash
git add cmd/stub.go
git commit -m "feat(cmd): add stubRunE helper for M5 placeholders"
```

---

## Task 1: `canopy worktree` parent + `list` / `new` / `prune` leaves

**Files:**
- Create: `cmd/worktree.go`
- Create: `cmd/worktree_test.go`

- [ ] **Step 1: Write the failing tests**

Write `cmd/worktree_test.go`:

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestWorktreeList_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy worktree list: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestWorktreeNew_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "new"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy worktree new: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestWorktreePrune_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "prune"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy worktree prune: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

// TestWorktree_HelpListsChildren verifies that invoking the parent without
// a leaf falls through to cobra's auto-generated help, which must mention
// each child subcommand by name.
func TestWorktree_HelpListsChildren(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"worktree", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, child := range []string{"list", "new", "prune"} {
		if !strings.Contains(got, child) {
			t.Errorf("worktree --help missing child %q; got %q", child, got)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd -run TestWorktree -race`
Expected: build error — `undefined: worktreeCmd` is not produced because there's no reference yet; instead cobra will return `Error: unknown command "worktree"` and the asserts will fail. Either failure mode confirms the tests are wired correctly. Note the failure mode before continuing.

- [ ] **Step 3: Write `cmd/worktree.go`**

```go
package cmd

import (
	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Inspect and manage git worktrees",
	Long: `worktree groups the worktree-management commands canopy will eventually
expose from its operational TUI (list, new, prune). In M5 each leaf is a
placeholder that prints a not-implemented message.`,
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List git worktrees and their state",
	Long: `list will print the worktrees canopy knows about along with branch,
ahead/behind, and dirty status. Placeholder in M5.`,
	RunE: stubRunE("worktree list"),
}

var worktreeNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new worktree",
	Long: `new will scaffold a new worktree for a branch off the main repo.
Placeholder in M5.`,
	RunE: stubRunE("worktree new"),
}

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove worktrees that no longer have an upstream",
	Long: `prune will remove worktrees whose branch has been merged or deleted
upstream. Placeholder in M5; the standalone "canopy prune" command will
eventually call into the same path.`,
	RunE: stubRunE("worktree prune"),
}

func init() {
	worktreeCmd.AddCommand(worktreeListCmd, worktreeNewCmd, worktreePruneCmd)
	rootCmd.AddCommand(worktreeCmd)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd -run TestWorktree -race -v`
Expected: PASS for `TestWorktreeList_PrintsStub`, `TestWorktreeNew_PrintsStub`, `TestWorktreePrune_PrintsStub`, `TestWorktree_HelpListsChildren`.

- [ ] **Step 5: Commit**

```bash
git add cmd/worktree.go cmd/worktree_test.go
git commit -m "feat(cmd): scaffold canopy worktree subcommand stubs"
```

---

## Task 2: `canopy sessions` parent + `list` / `tail` leaves

**Files:**
- Create: `cmd/sessions.go`
- Create: `cmd/sessions_test.go`

- [ ] **Step 1: Write the failing tests**

Write `cmd/sessions_test.go`:

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestSessionsList_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"sessions", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy sessions list: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestSessionsTail_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"sessions", "tail"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy sessions tail: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestSessions_HelpListsChildren(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"sessions", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, child := range []string{"list", "tail"} {
		if !strings.Contains(got, child) {
			t.Errorf("sessions --help missing child %q; got %q", child, got)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd -run TestSessions -race`
Expected: failures with "unknown command \"sessions\"" surfaced via the stub-message asserts. Confirm and continue.

- [ ] **Step 3: Write `cmd/sessions.go`**

```go
package cmd

import (
	"github.com/spf13/cobra"
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Inspect Claude Code sessions",
	Long: `sessions groups the read-only Claude Code session commands canopy
will expose alongside the TUI (list, tail). In M5 each leaf is a placeholder
that prints a not-implemented message.`,
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Claude Code sessions indexed by canopy",
	Long: `list will enumerate sessions across ~/.claude/projects with their
project, last-event time, and event count. Placeholder in M5.`,
	RunE: stubRunE("sessions list"),
}

var sessionsTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Stream new events from a Claude Code session",
	Long: `tail will follow a session's JSONL log and print normalized events as
they arrive. Placeholder in M5.`,
	RunE: stubRunE("sessions tail"),
}

func init() {
	sessionsCmd.AddCommand(sessionsListCmd, sessionsTailCmd)
	rootCmd.AddCommand(sessionsCmd)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd -run TestSessions -race -v`
Expected: PASS for all three `TestSessions*` tests.

- [ ] **Step 5: Commit**

```bash
git add cmd/sessions.go cmd/sessions_test.go
git commit -m "feat(cmd): scaffold canopy sessions subcommand stubs"
```

---

## Task 3: standalone `canopy prune`

**Files:**
- Create: `cmd/prune.go`
- Create: `cmd/prune_test.go`

- [ ] **Step 1: Write the failing test**

Write `cmd/prune_test.go`:

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrune_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"prune"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy prune: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd -run TestPrune_PrintsStub -race`
Expected: failure with "unknown command \"prune\"" surfaced via the assert. Confirm and continue.

- [ ] **Step 3: Write `cmd/prune.go`**

```go
package cmd

import (
	"github.com/spf13/cobra"
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove worktrees whose upstream branch is gone",
	Long: `prune is a top-level shortcut for the worktree-prune action. The
eventual implementation will call into the same code path as
"canopy worktree prune". Placeholder in M5.`,
	RunE: stubRunE("prune"),
}

func init() {
	rootCmd.AddCommand(pruneCmd)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd -run TestPrune_PrintsStub -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/prune.go cmd/prune_test.go
git commit -m "feat(cmd): scaffold canopy prune subcommand stub"
```

---

## Task 4: root help must advertise the new subcommands

**Files:**
- Modify: `cmd/root_test.go` (append a new test function — leave `TestRootCommand_Help` untouched)

- [ ] **Step 1: Append the failing test**

Add to the bottom of `cmd/root_test.go` (after the existing `TestRootCommand_Help`):

```go
// TestRootCommand_Help_ListsSubcommands asserts that `canopy --help` lists
// each top-level subcommand scaffolded in M5. This guards the acceptance
// criterion "canopy --help shows the subcommand tree" from issue #6.
func TestRootCommand_Help_ListsSubcommands(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, name := range []string{"worktree", "sessions", "prune"} {
		if !strings.Contains(got, name) {
			t.Errorf("--help missing subcommand %q; got %q", name, got)
		}
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./cmd -run TestRootCommand_Help_ListsSubcommands -race -v`
Expected: PASS. (Because Tasks 1–3 already registered the subcommands on `rootCmd` via `init()`, this test should pass without further code changes. If it fails, re-check that each subcommand file's `init()` actually calls `rootCmd.AddCommand`.)

- [ ] **Step 3: Commit**

```bash
git add cmd/root_test.go
git commit -m "test(cmd): assert root help lists M5 subcommands"
```

---

## Task 5: full verification + simplify pass

- [ ] **Step 1: Run the full test suite with the race detector**

Run: `go test ./... -race`
Expected: all packages PASS. Pay particular attention to `cmd/...` — every new test should be listed.

- [ ] **Step 2: Static check**

Run: `go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Manual smoke of each stub against the real binary**

Run:

```bash
go build -o canopy .
./canopy worktree list
./canopy worktree new
./canopy worktree prune
./canopy sessions list
./canopy sessions tail
./canopy prune
./canopy --help
./canopy worktree --help
./canopy sessions --help
```

Expected:
- Each leaf invocation prints exactly `canopy <name>: not yet implemented (M5 placeholder)` on stderr and exits 0.
- `./canopy --help` lists `demo`, `prune`, `sessions`, and `worktree` under "Available Commands".
- `./canopy worktree --help` lists `list`, `new`, `prune` as children.
- `./canopy sessions --help` lists `list`, `tail` as children.

If anything diverges, revisit the corresponding task and re-run that task's tests before moving on.

- [ ] **Step 4: Run the `/simplify` skill over the diff**

Per `CLAUDE.md` ("After committing and before opening a PR, run the `/simplify` skill over the diff…"), invoke `/simplify` against the branch diff. Address anything it surfaces with focused commits — do not bundle unrelated cleanups.

- [ ] **Step 5: Re-run tests after any simplify changes**

Run: `go test ./... -race && go vet ./...`
Expected: green.

---

## Task 6: push and open the PR

- [ ] **Step 1: Push the branch**

```bash
git push -u origin feat/cmd-subcommand-scaffolding
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --title "feat(cmd): scaffold M5 cobra subcommands (worktree/sessions/prune)" --body "$(cat <<'EOF'
## Summary
- Adds empty cobra subcommand stubs for `canopy worktree {list,new,prune}`, `canopy sessions {list,tail}`, and standalone `canopy prune`, each printing `canopy <name>: not yet implemented (M5 placeholder)` and exiting 0.
- Introduces a tiny `stubRunE` helper in `cmd/stub.go` so the placeholder text stays uniform and is trivial to grep & remove as real implementations land.
- Wires every new command into `rootCmd` via per-file `init()`, matching the existing `demo` pattern.

## Test plan
- [ ] `go test ./... -race` green (includes new stub + parent-help tests under `cmd/`)
- [ ] `go vet ./...` clean
- [ ] Manually run each `./canopy …` invocation listed in Task 5 Step 3 and confirm the expected stub message and exit code
- [ ] `./canopy --help` shows `worktree`, `sessions`, `prune` in Available Commands

Closes #6.
EOF
)"
```

- [ ] **Step 3: Return the PR URL**

Print the URL produced by `gh pr create` so it can be reviewed.

---

## Self-review notes

- **Spec coverage:** Each item in the issue body maps to a task — `cmd/worktree.go` (Task 1), `cmd/sessions.go` (Task 2), `cmd/prune.go` (Task 3), root help advertising the tree (Task 4), `go test ./cmd/... -race` and `go vet ./...` (Task 5).
- **Stub message format:** Exactly `canopy <name>: not yet implemented (M5 placeholder)` — matches the issue body's "What 'stub' means" section.
- **Test naming:** The issue references the legacy `TestRootCommand_PrintsStub`, which was renamed to `TestRootCommand_Help` in PR #15. New tests follow the current `TestRootCommand_Help` pattern (drive `rootCmd` with `SetArgs`, capture output via `SetOut`/`SetErr`), not the legacy name.
- **Out of scope:** No real implementations of `list`/`new`/`prune`/`tail`. No changes to `cmd/root.go`, `cmd/demo.go`, or any non-`cmd` package. No docs edits beyond this plan.
