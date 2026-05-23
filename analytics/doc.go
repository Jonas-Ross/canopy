// Package analytics runs aggregate queries over the sessions data layer:
// per-day token spend, tool-call distribution by model, per-session
// summaries, per-worktree session counts. It exists so the TUI's
// forensics tab (and, eventually, CLI subcommands) can render these
// views without leaking bucketing logic into the pure sessions/ package
// (see CLAUDE.md hard rules) and without coupling the aggregator's
// per-worktree snapshot to historical analytics.
//
// Inputs are always a *sessions.Store + a time window. Hydrate is called
// lazily; queries are pure functions over the resulting Session set.
package analytics
