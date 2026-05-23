# Canopy

A personal Go TUI (bubbletea + lipgloss) that fuses a worktree-aware git command center with Claude Code session forensics, for Jonas.

`AGENTS.md` is a symlink to this file — agents that look for either will find the same content.

## Read before changing anything

- `docs/validation.md` — required reading before touching anything in `tui/` or `cmd/demo*`. Explains the golden-frame harness, the `canopy demo` sandbox subcommand, the script grammar, and the cascade-timing rules.
- `docs/jsonl-schema.md` — required before non-trivial `sessions/` parser work. The shape of `~/.claude/projects/*/*.jsonl` as actually observed in production.
- `docs/sessions-interface.md` — the locked-in `sessions` package API and the reasoning behind each entrypoint.
- GitHub issue #14 — v2 tracker. Source of truth for what's planned beyond v1.

## Commands

| Command | Use |
|---|---|
| `go build ./...` | Build all packages |
| `go test ./... -race` | Run tests with the race detector (what CI gates on) |
| `go vet ./...` | Static checks |
| `go mod tidy` | Sync deps after touching `go.mod` |
| `./canopy` | Launch the TUI for the current repo |
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
- **`scenarioAnalytics()` (in `tui/golden_helpers_test.go`) hardcodes `Duration` / `TotalTime` on its fixtures**, so changes to `analytics/` calculation logic don't ripple into TUI golden tests. Validate analytics-layer changes with `./canopy demo`, not goldens.
- **`buildAnalyticsModel(t, view, snap)` (same file) translates `tui.ViewX` to digit-key sends** via a hardcoded switch. When reordering forensics sub-tabs in `tui/forensics.go`, update this helper's switch in sync — otherwise every analytics-view golden silently renders the wrong sub-view.
- **`tui/testdata/scripts/forensics_open.txt` couples sub-tab digit-key sends to capture filenames.** When the forensics sub-tab order changes, update both — the keys AND the per-view filenames — or the captures land under misleading names.

## Architecture

Layered, with `sessions` as a pure data-access library at the bottom and `tui` on top. The `aggregator` is the only layer that knows about everything. Correlation between worktrees and sessions happens via `CwdPrefix` (prefix match — handles nested dirs and monorepos; exact match is just `CwdPrefix == fullpath`).

- `sessions/` — Claude Code JSONL parsing/indexing. Surface: `Open`, `Sessions`, `Query`, `Hydrate`, `Events`, `Tail`. Pure.
- `git/` — worktree enumeration, status, ahead/behind. Shells out to `git`.
- `procs/` — process listing by cwd. macOS-first (sysctl `kern.proc.all` + `proc_pidinfo` + `KERN_PROCARGS2`, no cgo); Linux supported (`/proc/*/cwd`). Other platforms return `ErrUnsupported` and the aggregator soft-degrades.
- `pr/` — `gh` CLI wrapper with a 30s cache. Single `gh pr list --json …` per repo.
- `aggregator/` — joins all four sources into per-worktree state. Owns `CwdPrefix` correlation. Provides `Snapshot` and `Subscribe`.
- `tui/` — bubbletea views. Operational tab today; analytical tab is v2. `prettyModelName` (in `forensics_tools.go`) is the canonical display helper for model identifiers (`claude-opus-4-7` → `Opus 4.7`, drops date suffixes) — use it wherever model names appear in user-facing text.
- `cmd/` — cobra entrypoints. `demo` is real; `worktree`, `prune`, `sessions` are intentional stubs (`stub.go` → "not yet implemented") so the surface can grow without a rewrite.
- `internal/ansi/` — strips ANSI escapes for golden comparisons.
- `internal/demo/` — sandbox repo + fixture setup that the `demo` subcommand drives.

## Hard rules

- No domain logic in `sessions/`. No `os/exec`, no git, no pricing, no UI. If you reach for those, you're in the wrong package.
- No kitchen-sink dependencies. Discuss before adding any library.
- Shell out to `git`. Do not import `go-git` until shelling out actually hurts.
- Use the `gh` CLI for PR/CI state. No GitHub SDK.
- `CwdPrefix` is the worktree↔session correlation key. Prefix match, not exact.
- Tests are first-class. Every package gets tests. CI gates on `go test -race`.
- No daemon yet. Design the data layer so a daemon is a future plumbing addition, not a redesign — this is an open v2 question.
- macOS-first; Linux supported. Other platforms degrade gracefully — never crash on missing OS support.
- Cobra-backed CLI. Even single-purpose subcommands go through the cobra surface so `canopy worktree`, `canopy sessions`, `canopy prune` etc. can grow alongside.
- Anthropic's JSONL schema is theirs to change. Normalize into a stable internal `Event` type so there's one place to fix when it shifts.

## How to work with Jonas

- Pressure-test designs before code. Flag inconsistencies, missing edge cases, things he'll regret.
- Do not run ahead. No implementing multiple packages before you've talked shape.
- Slow is smooth, smooth is fast. Design conversation first, implementation second.
- If domain logic starts leaking into `sessions/`, say so.
- Stay opinionated about small/understandable code over large libraries.
- Aesthetics are a first-class feature, not polish at the end.
- Do not use the `superpowers` plugin (brainstorming / writing-plans / writing-specs / subagent-driven-development) on this project. The overhead doesn't fit how Jonas works here — go straight from conversation to implementation.

## Issue scoping

- When implementing a GitHub issue, **ship the full scope of the issue**. Do not silently carve off "later slices" or invent follow-up phases to make a PR feel smaller. If the work is genuinely too large for one PR, surface the split explicitly to Jonas before writing the spec — don't decide unilaterally.
- "Pause for demo" or similar markers in an issue body are **mid-flight checkpoints** for Jonas to look at the work and steer, not PR cut-points. Continue to the issue's full acceptance criteria after the checkpoint unless he tells you otherwise.
- Acceptance criteria like "would I open `canopy` tomorrow morning?" or "self-demo" are real bars, not flavor text. If shipped scope doesn't meet them, the issue is not done.
- Don't prime the PM sub-agent with "user prefers small PRs" — that's a value judgment that biases scope. Let the issue dictate scope; let Jonas dictate splits.

## Status

v1 shipped and in daily use. All packages — `sessions`, `git`, `procs`, `pr`, `aggregator`, `tui`, and `cmd/demo` — in place. The validation loop (`go test ./tui` goldens + `canopy demo` scripted replays + optional `--capture-png` via `freeze`) is the merge gate for TUI work; see `docs/validation.md`.

v2 in progress. The analytical/forensics tab (issue #55) landed as the first v2 feature: second top-level tab, four sub-views (spend / sessions / tools / worktrees), backed by the new `analytics/` package over the existing `sessions` data layer. Remaining v2 work tracked in #14: cross-linking operational ↔ analytical, activity feed, upstream-collision detection, stale-worktree triage, deeper PR integration, stack-branch awareness, port-collision warnings, agent session control, notifications. Daemon mode is decided (no daemon for v2 — see `docs/daemon-decision.md`).
