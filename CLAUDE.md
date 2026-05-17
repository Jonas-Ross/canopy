# Canopy

A personal Go TUI (bubbletea + lipgloss) that fuses a worktree-aware git command center with Claude Code session forensics, for Jonas.

## Read before changing anything

- `docs/handoff.md` — vision, architecture, design decisions, non-goals. Source of truth. Read it before any non-trivial change; do not duplicate it here.
- `~/.claude/plans/ok-claude-let-s-plan-agile-cocoa.md` — the approved v1 build-order plan (M0–M6). Defines milestone gates and exit criteria.

If those two disagree, ask Jonas — don't pick.

## Commands

| Command | Use |
|---|---|
| `go build ./...` | Build all packages |
| `go test ./... -race` | Run tests with the race detector (what CI gates on) |
| `go vet ./...` | Static checks |
| `go mod tidy` | Sync deps after touching `go.mod` |
| `./canopy` | Run the binary (currently prints a stub) |

CI: `.github/workflows/ci.yml` runs `go vet`, `go build`, and `go test -race -coverprofile=coverage.out` on Go 1.24 against every push to `main` and every PR.

## Git workflow

- Work on a feature branch off `main`. Never commit directly on `main`.
- Branch names: `feat/<slug>`, `fix/<slug>`, `docs/<slug>`, `refactor/<slug>`, `chore/<slug>`. Lowercase, hyphenated.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/): `<type>(<scope>): <subject>`. Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`, `build`, `ci`. Scope is the package or area (`sessions`, `cmd`, `docs`, etc.). Subject in imperative mood, lowercase, no trailing period.
- Keep commits as logical chunks — one concern per commit. Each commit should leave the build green where reasonable; bisectability is preferred but not strict.
- Pushing feature branches and opening PRs is part of the normal flow — no per-action confirmation needed. Never push directly to `main`, never force-push, never merge without explicit ask.
- Do not amend committed work; create a new commit.

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

## Starting a session

1. Skim `docs/handoff.md` if anything is unclear about intent.
2. Open the plan (`~/.claude/plans/ok-claude-let-s-plan-agile-cocoa.md`) and identify the current milestone (see Status below).
3. Confirm what's in scope for that milestone. Ask before deviating.
4. Surface design questions before writing code.

## Status

v0. Scaffolding only: cobra root command stub, single passing test, CI wired. Active milestone: **M0** (resolve the six open questions from the handoff before any package code lands). Update this section as milestones move.

Build order: M0 → M1 (`sessions`) → M2 (`git`, `procs`, `pr`, parallelizable) → M3 (`aggregator`) → M4 (TUI operational view) → M5 (subcommand stubs) → M6 (verification). Critical path is M0 → M1 → M3 → M4.
