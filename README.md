# Canopy

> An overhead view of the whole forest of your work — worktrees, agent sessions, PRs, processes, all at once.

Personal TUI built in Go (bubbletea + lipgloss) that fuses a worktree-aware git command center with Claude Code session forensics.

The repo is at the scaffolding stage. The full vision and design context live in [`docs/handoff.md`](docs/handoff.md).

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

M4 slices 1–3 landed: bubbletea operational view with worktree list, live-agent indicator (●), branch/dirty/ahead-behind/age columns, and incremental filter. Slices 4–8 (PR column, procs column, detail pane, ops keybinds, lipgloss polish) deferred to M4.5.
