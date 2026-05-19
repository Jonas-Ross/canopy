package sessions

import (
	"encoding/json"
	"strings"
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
func (r *ToolResult) String() string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	var parts []string
	for _, b := range r.Content {
		if b.Type == BlockText {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

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
// lines (no uuid / timestamp; the CLI rewrites them per turn). Field
// names mirror the production schema sampled from ~/.claude/projects;
// see docs/jsonl-schema.md §3. Populated by Hydrate; zero before.
//
// file-history-snapshot lines are deliberately not represented here:
// their payload is a snapshot blob rather than flat last-write-wins
// state. Unknown meta types fall through silently (forward-compat).
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

// PRLinkMeta carries the fields of a pr-link meta line. Zero value
// means no pr-link line was seen for this session.
type PRLinkMeta struct {
	Number     int    // from pr-link.prNumber
	URL        string // from pr-link.prUrl
	Repository string // from pr-link.prRepository
}

// WorktreeStateMeta carries the inner worktreeSession object of a
// worktree-state meta line. Zero value means no worktree-state line
// was seen.
type WorktreeStateMeta struct {
	OriginalCwd        string
	WorktreePath       string
	WorktreeName       string
	WorktreeBranch     string
	OriginalBranch     string
	OriginalHeadCommit string
}

// QueueOpMeta carries the operation and content of the most recent
// queue-operation meta line. Zero value means no queue-operation line
// was seen.
type QueueOpMeta struct {
	Operation string // from queue-operation.operation
	Content   string // from queue-operation.content
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

// OpenOption configures Open. Functional-option slot so background
// indexing / progress can be added without an API break.
type OpenOption func(*openConfig)

// OpenWithProgress registers a callback invoked as the index is built.
// In v1 the implementation may call it once at the end, or not at all;
// the slot exists so v2 can stream progress without a signature change.
func OpenWithProgress(fn func(loaded, total int)) OpenOption {
	return func(c *openConfig) { c.progress = fn }
}

type openConfig struct{ progress func(loaded, total int) }
