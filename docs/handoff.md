# Handoff: Canopy

> An overhead view of the whole forest of your work — worktrees, agent sessions, PRs, processes, all at once.

## What this is

Canopy is a personal TUI built in Go (bubbletea + lipgloss) that fuses two ideas into one tool:

1. **A worktree-aware git command center** — operational view of what's happening across all my parallel work right now.
2. **Claude Code session forensics** — analytical view across the history of agent sessions on this machine.

The insight driving the project: both ideas read the same underlying data (`~/.claude/projects/*/*.jsonl`), care about the same correlation key (working directory ↔ worktree), and want the same kind of polished bubbletea frontend. Building them as one tool unlocks the killer feature — cross-linking between live worktree state and the historical session record for that worktree. No standalone tool on the market does this because nobody else sits at this exact intersection.

This is personal tooling for me, built with Claude Code as the implementation partner. It can be opinionated, single-user, Jonas-shaped, and ambitious about what it does.

## Goals

- Build something I'll actually use every day — the bar is "earns a spot in my permanent workflow," not "interesting demo."
- Aim high on visual polish. This is a calling card for what bubbletea + lipgloss can do; treat aesthetics as a first-class feature.
- Get the data layer right early so the analytical view (v2) is mostly UI work, not parser rewrites.
- Move fast. With Claude Code driving implementation, the constraint isn't lines-of-code, it's clarity of design — most of my time goes into thinking, not typing.

## Non-goals

- Not a lazygit replacement. If Canopy starts reimplementing interactive rebase, we've lost the plot.
- Not a generic "agent observability platform." This is for me. No multi-user, no auth, no remote sync.

## Naming, binary, layout

- **Project name**: Canopy.
- **Binary**: `canopy`. No abbreviation — six letters types fine and `cnp` looks like a mistake.
- **Module path**: `github.com/jonas/canopy` (adjust to actual GitHub handle when initialized).
- **Subcommand-ready**: v1 is just `canopy` → launches the TUI, but the CLI surface should be structured so `canopy worktree …`, `canopy sessions …`, `canopy prune …` can grow into it later without a rewrite. Use cobra or similar from day one.

## Architecture sketch

Three internal packages, clean seams between them:

- **`sessions`** — owns parsing of Claude Code JSONL logs. Knows nothing about git, worktrees, pricing, or UI. Surface is small (Open, Sessions, Query, Hydrate, Events, Tail). I have a v1 sketch of this interface already; bring it up early and we'll refine before any implementation.
- **`aggregator`** (or whatever it ends up called) — sits on top of `sessions` and joins agent data to git/worktree state. This is where the worktree↔session correlation lives. Two consumers: the live "what's happening now per worktree" view and the historical "across all sessions ever" view.
- **`tui`** — bubbletea views. Tabs between an Operational view (worktree command center) and an Analytical view (forensics). Cross-link hotkey from a worktree to its filtered session history.

The seam that matters most: `sessions` is a pure data-access library. Everything domain-specific (worktrees, cost calc, PR state) lives above it. Don't let git logic leak into `sessions`.

## v1 — the worktree command center

The operational view. Concretely:

- **Worktree list** for the current repo (and optionally a configured set of repos).
- **Per-worktree state**: branch, dirty/clean + file count, ahead/behind upstream, last commit (relative time + subject), processes running with cwd inside it (especially `claude`), bound ports.
- **Live agent session indicator** per worktree: model in use, time elapsed, current token spend. Comes from `sessions.Tail`.
- **PR state per worktree**: PR number + title, draft/open/merged/closed state, CI summary (✓/✗/pending), review state at a glance. Load-bearing for cleanup decisions — see the dedicated section below.
- **Worktree lifecycle ops**: create a worktree from a branch, prune merged worktrees (single + batch), kill processes in a worktree, drop a shell.
- **Detail pane** for the focused worktree: full file diff stats, recent activity, PR details, the current/most-recent agent session for this worktree.
- **First-class aesthetics**. Lipgloss styling, rounded borders, adaptive light/dark colors, real attention to spacing and hierarchy. Merged worktrees should *look* mergeable-for-cleanup at a glance. Active agent sessions should *feel* alive.

Keybinds: enter to drop a shell, `n` create worktree, `d` prune (with confirm), `p` open PR in browser, `k` kill processes, `r` force refresh, `/` filter, `tab` switch to analytical view, `f` jump to forensics filtered by current worktree.

## PR integration — v1 design

Goal: enough PR state to make cleanup and "is this shippable" decisions confidently, plus the ergonomics to act on them.

**Approach**: `gh` CLI shellouts, parsed as JSON. No GitHub API client library — `gh` already handles auth, rate limits, and enterprise hosts, and shelling out is trivially mockable.

**Core query**: `gh pr list --json number,title,state,isDraft,headRefName,statusCheckRollup,reviewDecision,mergedAt,updatedAt,url --state all --limit 100`. One call per repo, gives us state for all branches at once, cache the result for ~30s. Per-worktree lookup is a map hit on `headRefName`.

**State surfaced per worktree**:

| Signal | Why it matters |
|---|---|
| Has PR? | Distinguishes in-flight from local-only branches |
| Draft / open / merged / closed | The cleanup signal |
| CI rollup | Is it actually shippable |
| Review decision | APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED |
| Merged when | "Merged 3 days ago, why is this worktree still here" |

**Visual treatment matters here.** A merged worktree should look merged — dimmed row, ✓ marker, age of merge visible, batch-prune candidate. An open PR with failing CI should be visible at a glance. APPROVED + green CI should be celebratory. This is where lipgloss earns its keep.

**Cleanup ergonomics**: select one or many worktrees with merged PRs, hit `d`, confirm, prune. Batch flow is in scope for v1 — it's the whole point of having merge state.

**Cache invalidation**: time-based (30s) for v1. Manual refresh covers the "I just pushed, update now" case. fsnotify on `.git/refs/heads/*` for local pushes is a nice-to-have we can layer in.

**Failure modes, handled gracefully**:
- No `gh` installed → degrade silently, just don't show PR columns.
- Not authenticated → one-time inline warning, then degrade.
- Network failure / rate limit → use stale cache, mark it stale visually.
- Branch has no PR → empty cell, not an error.

## v2 and beyond — the analytical view + ambitions

These aren't deferred-because-scary, they're deferred-because-they-build-on-v1. Order is rough priority.

- **Analytical / forensics tab.** Token spend over time, model comparison (Sonnet vs Opus on similar task shapes), tool-call distributions, retry/error hotspots, cost per merged PR, time-to-merge correlated with agent involvement. Most of this is queries + charts on the data layer that already exists.
- **Cross-linking from operational to analytical.** From a selected worktree, hotkey into forensics filtered to that worktree's `CwdPrefix`. The killer feature.
- **Activity feed.** Rolling timeline across worktrees — commits, pushes, PR state changes, agent sessions starting/ending. Becomes the "what's been happening" home screen.
- **Upstream-collision detection.** "You've been editing `queue.ts` and 3 commits just landed on main touching the same file." Prevents painful rebases.
- **Stale worktree triage view.** Automated suggestions: merged-and-old, no-commits-in-2-weeks, has-conflicts-with-main.
- **Deeper PR integration.** Review comments inline, CI failure log peek, "PRs waiting on my review across all repos" inbox.
- **Stack-branch awareness.** Detect when branches are stacked, surface the chain, support restacking when bases move. Don't reinvent Graphite but understand the concept.
- **Cross-worktree port collision warnings.** "Both `feat/ach` and `feat/event` want :3000."
- **Agent session control.** From the TUI: kill a runaway agent, peek at its current prompt/tool call, jump to its log. Requires either active wrapping of `claude` or a richer log-tailing protocol — design conversation, not implementation, yet.
- **Notifications.** Optional system notifications for PR state changes, CI completions, long-running agent sessions finishing.

## Key design decisions, baked in

| Decision | Reason |
|---|---|
| Go + bubbletea + lipgloss | Highest default polish of the TUI options. Charm ecosystem does aesthetic heavy lifting. |
| In-memory index of sessions, lazy event hydration | Discovery is O(files), hydration is O(bytes). Don't pay for what you don't display. Persistent index/cache is a later optimization. |
| Normalize JSONL into a stable internal `Event` type | Anthropic's schema is theirs to change. One place to fix when it shifts. |
| `Tail` returns a global event stream; filtering is the caller's job | Keep the primitive composable. Smart subscriptions are where bugs live. |
| `iter.Seq` for finite enumeration, channels for live tail | Idiomatic Go split — don't mix. |
| `CwdPrefix` is the worktree↔session correlation key | Prefix match handles nested dirs, monorepos, subprojects. Exact match is just `CwdPrefix=fullpath`. |
| No cost/pricing logic in `sessions` | Pricing drifts. Expose token counts; a separate `pricing` package can do math. |
| `gh` shellouts over a GitHub SDK | Auth, enterprise hosts, rate limits handled for free. Trivially mockable. |

## Open questions worth raising early

- **JSONL schema validation.** Before the parser is load-bearing, confirm the current shape of `~/.claude/projects/*/*.jsonl` against what we expect. Schemas drift. Worth 20 minutes with `jq` on real files first.
- **Refresh strategy for git state.** Polling vs. fsnotify on `.git/HEAD` + `.git/index` vs. hybrid. The hybrid (watch local, poll network-bound like `rev-list @{u}..`) is probably the right answer but adds state.
- **Process detection portability.** Walking `/proc/*/cwd` on Linux vs. `lsof` on macOS. I'm primarily on Linux/WSL2 — fine to make Linux first-class and stub macOS, but worth being explicit.
- **Daemon or not.** A background process opens up notifications, persistent activity feed, instant startup. v1 can be on-demand-only, but we should design the data layer such that promoting it to daemon-backed later is mostly plumbing.
- **Agent telemetry: passive log-reading vs. active CLI wrapping.** Passive (read JSONL) is cleanest and where v1 lives. Active wrapping unlocks kill-from-TUI and live prompts. Worth a design conversation early so we don't paint ourselves into a corner.
- **Multi-repo from v1?** I work across a few repos. v1 could be single-repo (cwd-based) or multi-repo (configured list). Multi-repo is more useful but bigger surface area for the worktree state model.

## What I want from Claude Code

I want to drive Canopy's planning and architecture in detail myself. Where Claude Code helps:

- Pressure-testing my designs before code gets written — flag inconsistencies, missing edge cases, things I'll regret.
- Implementing well-scoped pieces once we've agreed on shape.
- Catching bubbletea idioms or Go iter/concurrency patterns I'd get wrong from unfamiliarity.
- Suggesting where the design opens up an opportunity I haven't considered.
- Keeping the data layer clean — if I start letting domain logic leak into `sessions`, call it out.

What I don't want:

- Running ahead and implementing multiple packages before we've talked shape.
- Kitchen-sink dependencies pulled in without discussion — prefer small, understandable code over large libs.
- Implementation moving faster than the design conversation. Slow is smooth, smooth is fast.

## Stack reference

- Go (recent stable, `iter` package available).
- bubbletea + lipgloss + bubbles for TUI primitives.
- fsnotify for filesystem watching.
- `gh` CLI shellouts for PR/CI state.
- Shell out to `git` directly to start; reach for `go-git` only if it becomes annoying.

## Where to start

Read this doc, then ask what I want to talk about first. Most likely candidates:

1. Walk through the `sessions` package interface I've sketched and pressure-test it.
2. Sketch the full worktree state model — what the aggregator computes per worktree, and from what sources (git, processes, agent sessions, PR state).
3. v1 TUI layout and information architecture — list view, detail pane, how PR state surfaces visually, keybind set.
4. Plan the build order. With four major data sources (git, processes, sessions, PRs) feeding one view, sequencing matters.

Don't start coding until I say so.
