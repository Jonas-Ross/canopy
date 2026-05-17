// Package sessions reads Claude Code session logs from
// ~/.claude/projects/*/*.jsonl. It owns parsing and indexing; it knows
// nothing about git, worktrees, pricing, or UI.
//
// Open builds an in-memory index over a projects root by walking the
// top-level JSONL files and the per-session subagents subtree. The
// index is keyed by Session.ID; for subagent files the ID has the form
// "<parentSessionId>#<agentId>".
//
// The package surfaces an immutable view of session metadata for
// snapshot reads (Sessions, Session, Query, SessionsByCwdPrefix) and
// lazy hydration of bodies via Hydrate / Events. Live tailing is
// exposed as a channel of TailItem values.
//
// Usage sketch:
//
//	store, err := sessions.Open(filepath.Join(home, ".claude", "projects"))
//	if err != nil { return err }
//	defer store.Close()
//	for sess := range store.Sessions() {
//	    fmt.Println(sess.ID, sess.Model, sess.Cwds)
//	}
package sessions
