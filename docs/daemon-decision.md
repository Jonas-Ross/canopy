# Decision: no daemon for v2

**Status:** Decided (2026-05-23)

## Decision

Canopy stays single-process for the v2 feature set tracked in [#14](https://github.com/Jonas-Ross/canopy/issues/14). There will be no `canopyd` background daemon.

## Context

`CLAUDE.md` flagged daemon mode as the open architectural question that would "warp everything else" in v2. The v2 features in [#14](https://github.com/Jonas-Ross/canopy/issues/14) split cleanly on whether they require a process running when the TUI is closed:

| Feature | Needs a daemon? |
|---|---|
| Analytical / forensics tab | No — queries on existing data |
| Cross-linking operational → analytical | No — pure UI |
| Activity feed | Solvable on-disk (see below) |
| Upstream-collision detection | No — periodic refresh while open |
| Stale worktree triage | No |
| Deeper PR integration | No |
| Stack-branch awareness | No |
| Port-collision warnings | No |
| Notifications | Only if they fire when TUI is closed |
| Agent session control | Only if it acts when TUI is closed |

The activity feed's "what happened while I wasn't looking" need is satisfied by extending the on-disk pattern already established by the Tail offset checkpoint ([#12](https://github.com/Jonas-Ross/canopy/issues/12)) — aggregator snapshots + a rolling event log under `~/.cache/canopy/`. No resident process required. Tracked separately as a prereq for the activity-feed UI.

Notifications were the strongest argument for a daemon. They are explicitly scoped to **fire only while the TUI is open** — Jonas does not want background pings when Canopy isn't running. The same scoping applies to agent session control. With that scope, every v2 feature fits in single-process.

## Consequences

- All v2 work targets the existing single-process model. No IPC protocol, no lifecycle code, no launchd plist or systemd unit, no daemon-vs-TUI version skew, no second process to debug.
- Activity feed gets state-on-disk continuity but stays a TUI feature, not a daemon feature.
- The `aggregator.Subscribe` interface remains the seam every consumer goes through. If this decision is ever reopened, swapping the producer (in-process → socket) is a focused change, not a redesign. Keep that discipline.

## Trigger conditions that would reopen this

Revisit if any of the following become real requirements:

- Notifications that fire when the TUI is not running (e.g., "ping me when CI goes green even if Canopy is closed").
- A runaway-agent killswitch that must work without an active TUI session.
- Multiple concurrent TUI clients sharing state (e.g., one TUI per monitor showing different views of the same data).
- Cross-machine state sync.

None of these are on the table as of 2026-05-23.
