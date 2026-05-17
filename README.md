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

v0. Root command is a stub. See the handoff doc for the v1 plan.
