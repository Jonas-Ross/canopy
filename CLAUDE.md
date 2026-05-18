# Canopy

A personal Go TUI (bubbletea + lipgloss) that fuses a worktree-aware git command center with Claude Code session forensics, for Jonas.

`AGENTS.md` is a symlink to this file — agents that look for either will find the same content.

## Read before changing anything

- `docs/handoff.md` — vision, architecture, design decisions, non-goals. Source of truth. Read it before any non-trivial change; do not duplicate it here.
- `~/.claude/plans/ok-claude-let-s-plan-agile-cocoa.md` — the approved v1 build-order plan (M0–M6). Defines milestone gates and exit criteria.
- `docs/validation.md` — required reading before touching anything in `tui/` or `cmd/demo*`. Explains the golden-frame harness, the `canopy demo` sandbox subcommand, the script grammar, and the cascade-timing rules.

If those disagree, ask Jonas — don't pick.

## Commands

| Command | Use |
|---|---|
| `go build ./...` | Build all packages |
| `go test ./... -race` | Run tests with the race detector (what CI gates on) |
| `go vet ./...` | Static checks |
| `go mod tidy` | Sync deps after touching `go.mod` |
| `./canopy` | Run the binary (currently prints a stub) |
| `./canopy demo` | Launch the TUI against a throwaway sandbox repo |
| `./canopy demo --script=tui/testdata/scripts/<file>` | Replay a script and capture frames — the agent-driveable loop |
| `go test ./tui -update` | Re-bake golden frames after an intentional TUI change |

CI: `.github/workflows/ci.yml` runs `go vet`, `go build`, and `go test -race -coverprofile=coverage.out` on Go 1.24 against every push to `main` and every PR.

## Git workflow

- Work on a feature branch off `main`. Never commit directly on `main`.
- Branch names: `feat/<slug>`, `fix/<slug>`, `docs/<slug>`, `refactor/<slug>`, `chore/<slug>`. Lowercase, hyphenated.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/): `<type>(<scope>): <subject>`. Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`, `build`, `ci`. Scope is the package or area (`sessions`, `cmd`, `docs`, etc.). Subject in imperative mood, lowercase, no trailing period.
- Keep commits as logical chunks — one concern per commit. Each commit should leave the build green where reasonable; bisectability is preferred but not strict.
- After committing and before opening a PR, run the `/simplify` skill over the diff to catch reuse opportunities and quality issues; address what it surfaces.
- Pushing feature branches and opening PRs is part of the normal flow — no per-action confirmation needed. Never push directly to `main`, never force-push, never merge without explicit ask.
- Do not amend committed work; create a new commit.

## Testing

Tests are the merge gate, especially for TUI work. **`docs/validation.md` is the source of truth — read it before touching `tui/` or `cmd/demo*`.** Don't duplicate that doc here; the rules below are reminders.

- **TDD is required for every feature and bugfix.** Red → green → refactor, no exceptions:
  1. Write a failing test that pins the behavior you're about to add or fix. For a bug, the test must reproduce the bug and fail in the current tree before any production code changes.
  2. Run the test and confirm it fails for the right reason (not a typo, not a missing import). Note the failure mode before moving on.
  3. Write the minimum production code to make it pass. No drive-by features, no speculative abstractions.
  4. Run the full package's tests (and `go test ./... -race` before the commit) to confirm green, then refactor with the test as the safety net.
- **No "I'll add tests after" commits.** If you find yourself writing production code first, stop, revert, and start with the test. The only exception is exploratory spikes that you throw away before the real commit.
- **The three-tier loop, cheapest first:**
  1. `go test ./... -race` — golden frames catch layout/glyph/reflow regressions. <1s, CI-gated.
  2. `canopy demo --script=tui/testdata/scripts/<scenario>.txt` — exercises the full `cmd/root.go`-shaped pipeline against a sandbox. Catches wiring bugs goldens can't see.
  3. `canopy demo --script=… --capture-png=…` — pipes ANSI through `freeze` for visual checks (colour, bold, glyph vs solid block). Requires `freeze` on PATH.
- **When a golden changes:** read the diff, decide if it's intentional, then `go test ./tui -update` to re-bake and `git diff tui/testdata/golden/` to eyeball the new frames before committing. Goldens are still TDD-compatible: write or extend the demo script first, watch the golden diff fail, then make the code produce the frame you want.
- **Don't skip the demo loop on TUI changes.** Goldens prove the strings render; the demo script proves the pipeline wired them. Both, not either.
- **Don't loosen tests to make CI green.** If a test fails, it's telling you something. Fix the code, or update the golden when the change is intentional and reviewed.

## Architecture

Layered, with `sessions` as a pure data-access library at the bottom and `tui` on top. The `aggregator` is the only layer that knows about everything. Correlation between worktrees and sessions happens via `CwdPrefix` (prefix match — handles nested dirs and monorepos; exact match is just `CwdPrefix == fullpath`).

- `sessions/` — Claude Code JSONL parsing/indexing. Surface: `Open`, `Sessions`, `Query`, `Hydrate`, `Events`, `Tail`. Pure.
- `git/` — worktree enumeration, status, ahead/behind. Shells out to `git`.
- `procs/` — process listing by cwd. Linux-first (`/proc/*/cwd`); macOS stubbed with a build tag.
- `pr/` — `gh` CLI wrapper with a 30s cache. Single `gh pr list --json …` per repo.
- `aggregator/` — joins all four sources into per-worktree state. Owns `CwdPrefix` correlation. Provides `Snapshot` and `Subscribe`.
- `tui/` — bubbletea views. Operational tab in v1; analytical tab in v2.
- `cmd/` — cobra entrypoints. Subcommand stubs added in M5 so `canopy worktree …` etc. can grow in without a rewrite.

## Hard rules

- No domain logic in `sessions/`. No `os/exec`, no git, no pricing, no UI. If you reach for those, you're in the wrong package.
- No kitchen-sink dependencies. Discuss before adding any library.
- Shell out to `git`. Do not import `go-git` until shelling out actually hurts.
- Use the `gh` CLI for PR/CI state. No GitHub SDK.
- `CwdPrefix` is the worktree↔session correlation key. Prefix match, not exact.
- Tests are first-class. Every package gets tests. CI gates on `go test -race`.
- No daemon for v1. Design the data layer so a daemon is a future plumbing addition, not a redesign.
- Linux-first. macOS process detection is a stubbed build-tag file; degrade gracefully, don't crash.
- Cobra from day one, even when v1 is single-command. Subcommand surface should accommodate `canopy worktree`, `canopy sessions`, `canopy prune` later.
- Anthropic's JSONL schema is theirs to change. Normalize into a stable internal `Event` type so there's one place to fix when it shifts.

## How to work with Jonas

- Pressure-test designs before code. Flag inconsistencies, missing edge cases, things he'll regret.
- Do not run ahead. No implementing multiple packages before you've talked shape.
- Slow is smooth, smooth is fast. Design conversation first, implementation second.
- If domain logic starts leaking into `sessions/`, say so.
- Stay opinionated about small/understandable code over large libraries.
- Aesthetics are a first-class feature, not polish at the end — but only once the milestone gate says it's time.

## Issue scoping

- When implementing a GitHub issue, **ship the full scope of the issue**. Do not silently carve off "later slices" or invent follow-up milestones (e.g. "M4.5") to make a PR feel smaller. If the work is genuinely too large for one PR, surface the split explicitly to Jonas before writing the spec — don't decide unilaterally.
- "Pause for demo" or similar markers in an issue body are **mid-flight checkpoints** for Jonas to look at the work and steer, not PR cut-points. Continue to the issue's full acceptance criteria after the checkpoint unless he tells you otherwise.
- Acceptance criteria like "would I open `canopy` tomorrow morning?" or "self-demo" are real bars, not flavor text. If shipped scope doesn't meet them, the issue is not done.
- Don't prime the PM sub-agent with "user prefers small PRs" — that's a value judgment that biases scope. Let the issue dictate scope; let Jonas dictate splits.

## Starting a session

1. Skim `docs/handoff.md` if anything is unclear about intent.
2. Open the plan (`~/.claude/plans/ok-claude-let-s-plan-agile-cocoa.md`) and identify the current milestone (see Status below).
3. Confirm what's in scope for that milestone. Ask before deviating.
4. Surface design questions before writing code.

## Status

v0.4. Operational TUI shipped in PR #15 (with follow-up polish in #28–30): `sessions`, `git`, `procs`, `pr`, `aggregator`, `tui`, and `cmd/demo` all in place. The validation loop (`go test ./tui` goldens + `canopy demo` scripted replays + optional `--capture-png` via `freeze`) is the merge gate for further TUI work — see `docs/validation.md`. Active milestone: **M4 → M5** transition.

Build order: M0 → M1 (`sessions`) → M2 (`git`, `procs`, `pr`, parallelizable) → M3 (`aggregator`) → M4 (TUI operational view, **self-validating via the demo loop**) → M5 (subcommand stubs) → M6 (verification). Critical path is M0 → M1 → M3 → M4.
