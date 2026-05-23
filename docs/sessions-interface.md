# `sessions` package — v1 interface design (M1.0)

Design artifact for the v1 `sessions` Go package. Captures the locked-in
API shape that landed in the package. Read `docs/jsonl-schema.md`
(especially §10) and `CLAUDE.md` (§Architecture, §Hard rules) first; this
doc assumes both.

## Overview

`sessions` is a pure data-access library over `~/.claude/projects/*/*.jsonl`.
It owns parsing, indexing, lazy event hydration, and live tailing. It is
explicitly **not** git-aware, worktree-aware, pricing-aware, or UI-aware.
Anthropic's JSONL schema is theirs to change; this package is the single
seam that normalizes it into a stable internal shape.

## Design rules

- No domain logic. No `os/exec`, no git, no pricing, no UI imports.
- `iter.Seq` / `iter.Seq2` for finite enumeration; channels for live tail.
  Don't mix the two idioms.
- Anthropic schema isolation: callers see `Event`, never raw JSON shapes.
- Lazy hydration: `Open` indexes metadata only; bodies and full token
  aggregation come from `Hydrate` / `Events`.
- Pure: `Store` is safe for concurrent calls; `Hydrate` is single-flighted
  per session.

## Type definitions

```go
// Package sessions reads Claude Code session logs from
// ~/.claude/projects/*/*.jsonl. It owns parsing and indexing; it knows
// nothing about git, worktrees, pricing, or UI.
package sessions

import (
	"context"
	"encoding/json"
	"iter"
	"time"
)

// Event is the normalized form of one conversation event. Exactly one of
// the payload pointers is non-nil, selected by Kind.
type Event struct {
	SessionID  string
	UUID       string    // empty for meta lines that lack one
	ParentUUID string    // empty on session root
	Timestamp  time.Time // RFC3339 UTC; note: not strictly monotonic per file
	Kind       EventKind

	User            *UserMessage
	Assistant       *AssistantMessage
	ToolUse         *ToolUse
	ToolResult      *ToolResult
	CompactBoundary *CompactBoundary
}

// EventKind discriminates the populated payload pointer on Event.
//
// note: no Summary kind. The sketch had one; no type:"summary" exists
// in any sampled JSONL. CompactBoundary replaces it and is sourced from
// system events with subtype:"compact_boundary".
type EventKind uint8

const (
	EventUser EventKind = iota
	EventAssistant
	EventToolUse
	EventToolResult
	EventCompactBoundary
)

// UserMessage is a user-authored turn. Tool results ride as user lines in
// the raw JSONL but are surfaced as EventToolResult instead, not here.
type UserMessage struct {
	Text    string         // flattened text concatenation for ergonomics
	Content []ContentBlock // empty when the raw payload was a plain string
	Cwd     string         // cwd recorded on this line; may differ from Session.Cwds[0]
	Version string         // CLI version that wrote this line (e.g. "2.1.143")
	IsMeta  bool           // synthetic local-command caveats; hidden from Events() by default
}

// AssistantMessage is one assistant turn (or one block of a split turn).
// Tokens reflects this line's usage object; Hydrate dedupes by MessageID
// when aggregating into Session.Tokens.
type AssistantMessage struct {
	Model      string // e.g. "claude-opus-4-7"; may be the sentinel "<synthetic>"
	MessageID  string // Anthropic message.id — load-bearing for token dedup
	RequestID  string // Anthropic request id; may be empty
	Text       string // flattened text-block concatenation
	Tokens     TokenStats
	StopReason string // "tool_use" / "end_turn" / "stop_sequence" / ""
}

// ToolUse is one assistant-issued tool call. Input is raw JSON; each
// tool defines its own input schema and decoding is the caller's job.
type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult is the result of a tool call.
//
// note: ~9% of tool results in the wild are block arrays (text /
// tool_reference / image), not plain strings. Content preserves that
// faithfully; String() flattens to text and drops non-text blocks.
type ToolResult struct {
	ToolUseID string
	IsError   bool
	Content   []ContentBlock
}

// String flattens Content by concatenating text blocks (newline-separated).
// Non-text blocks are dropped.
func (r *ToolResult) String() string { /* impl */ return "" }

// ContentBlock is a tagged union of the block types found inside
// tool_result.content arrays (and other content arrays). Exactly one of
// the payload fields is meaningful, keyed by Type.
type ContentBlock struct {
	Type          ContentBlockType
	Text          string         // Type == BlockText
	ToolReference *ToolReference // Type == BlockToolReference
	Image         *ImageBlock    // Type == BlockImage
}

// ContentBlockType discriminates ContentBlock.
type ContentBlockType uint8

const (
	BlockText ContentBlockType = iota
	BlockToolReference
	BlockImage
)

// ToolReference is the compact reference block emitted inside some
// tool_result.content arrays (carries a tool name only).
type ToolReference struct {
	ToolName string
}

// ImageBlock is a base64-embedded image. The parser does not decode the
// data; callers that care can.
type ImageBlock struct {
	MediaType string
	Data      string // base64
}

// CompactBoundary marks where the CLI compacted a session. Pre- and
// post-compact segments must be accounted for separately in cost/time
// charts. Sourced from system events with subtype:"compact_boundary".
type CompactBoundary struct {
	PreCompactTokens  int
	PostCompactTokens int
	Trigger           string // e.g. "auto", "manual"
}

// TokenStats is the four-field token accounting used package-wide.
type TokenStats struct {
	Input         int
	Output        int
	CacheRead     int
	CacheCreation int
}

// Session is the metadata view of one JSONL file. Cheap at Open time;
// Tokens, Tools, EventCount, Meta, and the full Cwds set are filled by
// Hydrate.
//
// note: pointers returned by Sessions / Session / Query /
// SessionsByCwdPrefix are stable index entries owned by the Store.
// Callers must not mutate them — Hydrate is the only writer, and it is
// internally synchronized.
type Session struct {
	ID   string // for subagent files: "<parentSessionId>#<agentId>"
	Path string // absolute path to the JSONL file

	// Cwds is populated from the first and last conversation lines at
	// Open() (≤2 entries in ~all observed files). Hydrate replaces it
	// with the full distinct-cwd set in observation order.
	Cwds []string

	Model     string    // first non-"<synthetic>" model seen
	StartedAt time.Time // min timestamp across the file
	UpdatedAt time.Time // max timestamp across the file

	// Subagent linkage. IsSidechain is true for files under the
	// <sessionId>/subagents/ subtree. ParentSessionID is the bare
	// parent session id (no "#agentId" suffix), empty for top-level.
	IsSidechain     bool
	ParentSessionID string

	// Filled by Hydrate. Zero before Hydrate is called.
	EventCount int
	Tokens     TokenStats
	Tools      map[string]int // non-nil empty map after Hydrate; range-safe without nil check
	Meta       SessionMeta    // last-write-wins state from JSONL meta lines
}

// SessionMeta is the last-write-wins state expressed by JSONL meta
// lines (no uuid / timestamp; the CLI rewrites them per turn).
// Populated by Hydrate; zero before. Events() filters meta lines out
// of its output — Meta is the only surface exposing them.
//
// file-history-snapshot lines are deliberately not represented here:
// their payload is a snapshot blob, not flat last-write-wins state.
// Unknown meta types fall through silently (forward-compat).
type SessionMeta struct {
	LastPrompt     string            // from last-prompt.lastPrompt
	AITitle        string            // from ai-title.aiTitle
	CustomTitle    string            // from custom-title.customTitle
	PermissionMode string            // from permission-mode.permissionMode
	AgentName      string            // from agent-name.agentName
	AgentSetting   string            // from agent-setting.agentSetting
	PRLink         PRLinkMeta        // from pr-link
	WorktreeState  WorktreeStateMeta // from worktree-state.worktreeSession
	QueueOperation QueueOpMeta       // from queue-operation
}

type PRLinkMeta struct {
	Number     int    // pr-link.prNumber
	URL        string // pr-link.prUrl
	Repository string // pr-link.prRepository
}

type WorktreeStateMeta struct {
	OriginalCwd        string
	WorktreePath       string
	WorktreeName       string
	WorktreeBranch     string
	OriginalBranch     string
	OriginalHeadCommit string
}

type QueueOpMeta struct {
	Operation string // queue-operation.operation
	Content   string // queue-operation.content
}

// Query filters the session index. Zero-valued fields are wildcards.
//
// note: Since and Until are both zero-as-unbounded. The implementation
// clamps a zero Until internally so callers do not need to set it.
type Query struct {
	CwdPrefix string    // prefix match against any entry in Session.Cwds
	Since     time.Time // zero = unbounded lower
	Until     time.Time // zero = unbounded upper
	Model     string    // substring match on Session.Model
}

// TailItem is the unit delivered on the Tail channel. Either Event is
// meaningful or Err is non-nil; a non-nil Err is an fsnotify-level
// failure and the channel closes shortly after.
type TailItem struct {
	Event Event
	Err   error
}

// TailStats reports observability counters for the live tail.
type TailStats struct {
	Dropped uint64 // events discarded because the consumer was too slow
}

// TailOption configures a Tail call. Variadic slot so future tunables
// can be added without an API break.
type TailOption func(*tailConfig)

// TailWithCheckpoint persists per-file byte offsets to path so a
// subsequent Tail seeded with the same path resumes from the last
// persisted positions instead of current EOF. The file is written
// atomically (temp file + rename). Missing or corrupt files fall back
// to current-EOF seeding silently. Offsets flush every ~5s, every
// ~1000 processed events, and once on graceful teardown.
func TailWithCheckpoint(path string) TailOption { /* impl */ return nil }

// OpenOption configures Open. Functional-option slot so background
// indexing / progress can be added without an API break.
type OpenOption func(*openConfig)

// OpenWithProgress registers a callback invoked as the index is built.
// In v1 the implementation may call it once at the end, or not at all;
// the slot exists so v2 can stream progress without a signature change.
func OpenWithProgress(fn func(loaded, total int)) OpenOption { /* impl */ return nil }

type openConfig struct{ progress func(loaded, total int) }
```

## Store API

```go
// Store is the in-memory index over a Claude Code projects root. Safe
// for concurrent use by multiple goroutines.
type Store struct {
	// unexported: index map, sorted (cwd, sessionId) slice, fs watcher,
	// per-session single-flight gate, tail subscribers, drop counter, mu.
}

// Open builds the session index by walking <root>/*/*.jsonl plus the
// subagent subtree at <root>/<projectDir>/<sessionId>/subagents/. It
// reads first and last conversation lines per file to populate metadata;
// it does not read full event bodies. Synchronous: returns only after
// the index is built. Typical root: filepath.Join(home, ".claude", "projects").
func Open(root string, opts ...OpenOption) (*Store, error) { /* impl */ return nil, nil }

// Close releases the fs watcher and any background goroutines; live
// Tail channels created against this Store are closed.
func (s *Store) Close() error { /* impl */ return nil }

// Sessions returns all indexed sessions, unfiltered, in unspecified order.
// Returned pointers are stable index entries; do not mutate.
func (s *Store) Sessions() iter.Seq[*Session] { /* impl */ return nil }

// Session looks up one session by ID. For subagents, the ID is
// "<parentSessionId>#<agentId>".
func (s *Store) Session(id string) (*Session, error) { /* impl */ return nil, nil }

// Query enumerates sessions matching the filter. Zero-valued fields on
// Query are wildcards. Order is unspecified.
func (s *Store) Query(q Query) iter.Seq[*Session] { /* impl */ return nil }

// SessionsByCwdPrefix is the aggregator's hot-path correlation lookup:
// every indexed session whose Cwds contains an entry with the given
// prefix. Backed by an in-memory sorted (cwd, sessionId) slice with
// binary search; O(log n + matches) per call.
func (s *Store) SessionsByCwdPrefix(prefix string) []*Session { /* impl */ return nil }

// Hydrate fills the lazy fields on Session: EventCount, Tokens, Tools,
// Meta, and the full Cwds set. Idempotent. After Hydrate, Session.Tools
// is a non-nil (possibly empty) map and Session.Meta carries the
// last-write-wins state from JSONL meta lines.
//
// note: token aggregation dedupes by message.id before summing — the CLI
// emits one JSONL line per content block, each line carrying the full
// usage object. Naive summing triple-counts Opus turns.
//
// note: concurrent Hydrate calls for the same session are single-flighted
// internally. Two goroutines hydrating the same *Session collapse into
// one parse and both observe the same final state without racing.
func (s *Store) Hydrate(sess *Session) error { /* impl */ return nil }

// Events iterates the events of a session in file order. Malformed lines
// surface as a non-nil error from the Seq2; the caller decides whether
// to stop. Side-band line types (attachment, file-history-snapshot, …)
// are filtered by default; meta lines (last-prompt, ai-title, pr-link, …)
// are never surfaced as Events.
func (s *Store) Events(sessionID string) iter.Seq2[Event, error] { /* impl */ return nil }

// Tail returns a channel of live events across all sessions in the
// projects root, driven by fsnotify on the projects tree. fsnotify-level
// errors surface as TailItem{Err: ...} and the channel closes shortly
// after.
//
// Backpressure: bounded buffer (256). If the consumer falls behind,
// events are dropped rather than blocking the producer; the drop count
// is exposed via TailStats. Slow consumers are visible, never deadlocking.
//
// Cancellation: ctx cancellation closes the channel cleanly.
//
// TailOption: variadic functional options. TailWithCheckpoint(path)
// persists per-file byte offsets to disk so a subsequent Tail seeded
// with the same path resumes from the last persisted positions
// instead of current EOF, surviving restarts (and a future daemon
// mode) without missing events. Missing/corrupt checkpoint files fall
// back to current-EOF seeding silently.
func (s *Store) Tail(ctx context.Context, opts ...TailOption) (<-chan TailItem, error) { /* impl */ return nil, nil }

// TailStats reports observability counters for the live tail.
func (s *Store) TailStats() TailStats { /* impl */ return TailStats{} }
```

## Lifecycle / usage sketch

Pseudo-code for how the aggregator (M3) drives `sessions`:

```go
store, _ := sessions.Open(filepath.Join(home, ".claude", "projects"))
defer store.Close()

// Live updates: subscribe once, filter in the aggregator.
items, _ := store.Tail(ctx)
go func() {
	for it := range items {
		if it.Err != nil { log.Warn(it.Err); continue }
		dispatch(it.Event) // aggregator routes by SessionID -> worktree
	}
}()

// "What's running in this worktree right now": cheap snapshot.
var live *sessions.Session
for _, sess := range store.SessionsByCwdPrefix(worktreePath) {
	if time.Since(sess.UpdatedAt) < liveWindow { live = sess }
}

// Detail-pane focus: hydrate the one session the user is looking at.
_ = store.Hydrate(live)
for ev, err := range store.Events(live.ID) {
	if err != nil { continue }
	render(ev)
}
```

## Concurrency & error contract

- **Store** is safe for concurrent calls on every exported method.
- **Open** is synchronous and single-threaded. If index build cost
  exceeds ~500ms on real `~/.claude` data, `OpenWithProgress` is the seam
  for background indexing later — no API break required.
- **Hydrate** is single-flighted per session. Same session: one parse,
  both callers see the final state. Distinct sessions: parallel.
- **Events** surfaces malformed lines via the Seq2 error half; iteration
  does not panic on bad JSON. Caller decides whether to stop.
- **Tail** uses a bounded buffer (256) and drops on overflow. fsnotify
  errors arrive as `TailItem{Err: ...}` and the channel closes. `ctx`
  cancellation closes the channel cleanly.
- **Pointer stability**: `Sessions`, `Session`, `Query`, and
  `SessionsByCwdPrefix` return stable index entries owned by the Store.
  Callers must not mutate them.

## What this interface deliberately does not include

- **`file-history-snapshot` payload** — counted out of `Events` like
  the other side-band lines, and intentionally not modeled on
  `SessionMeta`. The payload is a snapshot blob, not flat last-write-
  wins state; a typed surface would require sampling more production
  fixtures first.
- **Attachment / system subtype filtering policy** — parser-internal, not
  interface. Default: drop the noisy side-band; promote `compact_boundary`.
- **`<synthetic>` model sentinel handling** — implementation detail of
  `AssistantMessage.Model`; callers see the literal string.
- **Pricing / cost math** — out of scope, never in this package. Per
  `CLAUDE.md` hard rules: no domain logic in `sessions/`.
- **Active CLI wrapping** (kill-from-TUI, live prompt peek) — passive
  log-reading only in v1. Active wrapping is a v2 design question (see
  GitHub issue #14 "Agent session control").

## Open questions for Jonas

Recommendation first; flag any to revise.

1. **Default-hide `isMeta:true` user lines from `Events`?** Recommend
   yes (local-command caveats are noise for forensics). If you later
   want them, prefer a sibling `EventsAll` over a global knob.
2. **Keep both `MessageID` and `RequestID` on `AssistantMessage`?**
   Recommend yes. `MessageID` is load-bearing for the dedup contract;
   `RequestID` is cheap to carry and useful for cross-referencing API
   logs. Drop `RequestID` if you'd rather keep the type minimal.
3. **`SessionsByCwdPrefix` return type: slice vs `iter.Seq[*Session]`?**
   Recommend slice. Bounded result size in practice, the aggregator
   wants a snapshot, easier to test against. Flag if you'd rather the
   iterator for consistency with `Sessions` / `Query`.
4. **`UserMessage.Text` convenience field alongside `Content`?** Recommend
   yes. Most user lines are plain strings; forcing every caller through
   the block array is needless friction. Documented that tool-result
   user lines use `Event.ToolResult` instead.
5. **`OpenWithProgress` slot in v1 at all?** Recommend keep, no-op
   implementation. Cost: one option type plus a nullable callback.
   Benefit: no API break when we later decide we want background
   indexing.
