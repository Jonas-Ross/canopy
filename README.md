# Canopy

> An overhead view of the whole forest of your work — worktrees, agent sessions, PRs, processes, all at once.

Personal TUI built in Go (bubbletea + lipgloss) that fuses a worktree-aware git command center with Claude Code session forensics.

v1 is shipped and in daily use. v2 (analytical tab, cross-linking, activity feed, and more) is tracked in [#14](https://github.com/Jonas-Ross/canopy/issues/14).

## Build

```sh
go mod tidy
go build ./...
./canopy
```

## Test

Tests are a first-class citizen — every package should have them, and CI gates on them.

```sh
go test ./...
```

CI (`.github/workflows/ci.yml`) runs `go vet`, `go build`, and `go test -race` on every push to `main` and every PR.

## Status

v1 shipped and in daily use. Operational TUI with worktree list, live-agent indicator (●), branch/dirty/ahead-behind/age columns, PR and procs columns, detail pane, ops keybinds, and incremental filter. v2 planning is happening in [#14](https://github.com/Jonas-Ross/canopy/issues/14).
