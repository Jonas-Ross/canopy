package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"strings"
	"time"
)

// events is the concrete iterator behind (*Store).Events. Kept in this
// file so the surface area touched in store.go is minimal.
//
// Contract:
//   - Unknown sessionID → yield exactly one (Event{}, ErrNotFound)
//     wrapped with the queried id, then stop.
//   - Otherwise: open the session's file, scan line by line. Each
//     parseable conversation event is mapped to one or more Events
//     (see eventsFromLine).
//   - Malformed JSON lines yield (Event{}, error) but iteration
//     continues; the receiver controls termination by returning false
//     from yield.
//   - isMeta:true lines are skipped silently (per the locked decision
//     in docs/sessions-interface.md "Open questions" Q1).
//   - Side-band line types (attachment, file-history-snapshot,
//     non-compact_boundary system events, all meta line types) are
//     skipped silently.
//   - The Event.SessionID field always carries the indexed Session.ID,
//     which for subagent files is the composite "<parent>#<agentId>"
//     form. This lets callers correlate Event.SessionID back to
//     Session.ID without ambiguity.
func (s *Store) events(sessionID string) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		sess, err := s.Session(sessionID)
		if err != nil {
			yield(Event{}, err)
			return
		}

		f, err := os.Open(sess.Path)
		if err != nil {
			yield(Event{}, fmt.Errorf("sessions: open %s: %w", sess.Path, err))
			return
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), maxScanLineSize)

		lineNum := 0
		for sc.Scan() {
			lineNum++
			line := sc.Bytes()
			if isBlank(line) {
				continue
			}

			// Copy bytes — Scanner.Bytes() is reused across iterations.
			buf := make([]byte, len(line))
			copy(buf, line)

			events, perr := eventsFromLine(buf, sess.ID)
			if perr != nil {
				if !yield(Event{}, fmt.Errorf("sessions: malformed line %d in %s: %w", lineNum, sess.Path, perr)) {
					return
				}
				continue
			}
			for _, ev := range events {
				if !yield(ev, nil) {
					return
				}
			}
		}
		if err := sc.Err(); err != nil {
			yield(Event{}, fmt.Errorf("sessions: scan %s: %w", sess.Path, err))
			return
		}
	}
}

// isBlank reports whether the line is empty or only whitespace.
func isBlank(line []byte) bool {
	for _, b := range line {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return false
		}
	}
	return true
}

// eventLine is the projection of a single JSONL line needed for event
// classification. Fields not used for mapping are deliberately omitted
// to keep the unmarshal cost low.
type eventLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	UUID            string          `json:"uuid"`
	ParentUUID      string          `json:"parentUuid"`
	Timestamp       string          `json:"timestamp"`
	Cwd             string          `json:"cwd"`
	Version         string          `json:"version"`
	RequestID       string          `json:"requestId"`
	IsMeta          *bool           `json:"isMeta"`
	Message         json.RawMessage `json:"message"`
	CompactMetadata json.RawMessage `json:"compactMetadata"`
}

// rawAssistantMessage is the inner assistant message envelope.
type rawAssistantMessage struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      *rawUsage       `json:"usage"`
	IsMeta    *bool            `json:"isMeta"`
}

// rawUserMessage is the inner user message envelope. Content can be a
// string or an array of blocks; we keep it raw and dispatch below.
type rawUserMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	IsMeta  *bool           `json:"isMeta"`
}

// rawUsage mirrors message.usage. Fields not in TokenStats are ignored.
type rawUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// rawContentBlock is the discriminated union for content blocks. Only
// the fields used by the parser are decoded; unknown ones (e.g.
// thinking.signature) are ignored.
type rawContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
	// tool_use fields
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_reference field
	ToolName string `json:"tool_name"`
	// image field
	Source *rawImageSource `json:"source"`
}

type rawImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// rawCompactMetadata mirrors compactMetadata on system+compact_boundary
// lines. Field names follow docs/jsonl-schema.md §8 item 2:
// trigger / preTokens / postTokens.
type rawCompactMetadata struct {
	Trigger    string `json:"trigger"`
	PreTokens  int    `json:"preTokens"`
	PostTokens int    `json:"postTokens"`
}

// eventsFromLine classifies one JSONL line and returns zero or more
// Events. An empty result with nil error means the line was an
// intentional skip (meta, side-band, etc). A non-nil error means the
// line failed to unmarshal somewhere structural.
func eventsFromLine(line []byte, sessionID string) ([]Event, error) {
	var el eventLine
	if err := json.Unmarshal(line, &el); err != nil {
		return nil, err
	}

	// Top-level isMeta filter (locked decision: default-hide).
	if el.IsMeta != nil && *el.IsMeta {
		return nil, nil
	}

	switch el.Type {
	case "user":
		return userEvents(&el, sessionID)
	case "assistant":
		return assistantEvents(&el, sessionID)
	case "system":
		if el.Subtype == "compact_boundary" {
			return compactBoundaryEvents(&el, sessionID)
		}
		return nil, nil
	default:
		// attachment, file-history-snapshot, meta line types, etc. —
		// not in v1 Events scope.
		return nil, nil
	}
}

// userEvents handles type:"user" lines. A user line whose content
// includes any tool_result block is surfaced as one EventToolResult per
// tool_result block instead of an EventUser (per docs §3/§5/§7).
// Otherwise it yields a single EventUser whose Content is the
// normalized block array (a bare-string payload becomes one BlockText).
func userEvents(el *eventLine, sessionID string) ([]Event, error) {
	var msg rawUserMessage
	if len(el.Message) > 0 {
		if err := json.Unmarshal(el.Message, &msg); err != nil {
			return nil, fmt.Errorf("user.message: %w", err)
		}
	}
	// message.isMeta also gets the default-hide treatment.
	if msg.IsMeta != nil && *msg.IsMeta {
		return nil, nil
	}

	blocks, isString, err := decodeUserContent(msg.Content)
	if err != nil {
		return nil, fmt.Errorf("user.message.content: %w", err)
	}

	ts, _ := parseTimestamp(el.Timestamp)

	// Tool-result branch: if the content is an array containing any
	// tool_result block, surface as EventToolResult event(s).
	if !isString && hasToolResult(msg.Content) {
		return toolResultEvents(msg.Content, el, ts, sessionID)
	}

	return []Event{{
		SessionID:  sessionID,
		UUID:       el.UUID,
		ParentUUID: el.ParentUUID,
		Timestamp:  ts,
		Kind:       EventUser,
		User: &UserMessage{
			Text:    flattenTextBlocks(blocks),
			Content: blocks,
			Cwd:     el.Cwd,
			Version: el.Version,
			IsMeta:  false, // meta lines were filtered above
		},
	}}, nil
}

// decodeUserContent normalizes message.content into a []ContentBlock.
// A bare-string payload becomes a single BlockText. Returns
// (blocks, wasString, error). blocks may be nil for explicit null or
// absent payloads.
//
// When the input was a JSON array, Content is the typed []ContentBlock;
// the wasString flag is false so callers can distinguish the two cases.
func decodeUserContent(raw json.RawMessage) ([]ContentBlock, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := trimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, true, err
		}
		return []ContentBlock{{Type: BlockText, Text: s}}, true, nil
	case '[':
		blocks, err := decodeBlocks(raw)
		if err != nil {
			return nil, false, err
		}
		return blocks, false, nil
	case 'n': // null
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("unexpected content shape")
	}
}

// hasToolResult returns true iff raw is a JSON array containing at
// least one block whose type is "tool_result".
func hasToolResult(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var arr []rawContentBlock
	if err := json.Unmarshal(raw, &arr); err != nil {
		return false
	}
	for _, b := range arr {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// toolResultEvents emits one EventToolResult per tool_result block in
// the user message content array. Non-tool_result blocks on the same
// line are ignored: per the interface doc a tool-result user line is
// the result line, and mixed content with tool_result is treated as a
// result event.
func toolResultEvents(raw json.RawMessage, el *eventLine, ts time.Time, sessionID string) ([]Event, error) {
	var arr []rawContentBlock
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("user.message.content array: %w", err)
	}
	var out []Event
	for _, rb := range arr {
		if rb.Type != "tool_result" {
			continue
		}
		blocks, err := decodeToolResultContent(rb.Content)
		if err != nil {
			return nil, fmt.Errorf("tool_result.content: %w", err)
		}
		out = append(out, Event{
			SessionID:  sessionID,
			UUID:       el.UUID,
			ParentUUID: el.ParentUUID,
			Timestamp:  ts,
			Kind:       EventToolResult,
			ToolResult: &ToolResult{
				ToolUseID: rb.ToolUseID,
				IsError:   rb.IsError,
				Content:   blocks,
			},
		})
	}
	return out, nil
}

// decodeToolResultContent normalizes the polymorphic tool_result.content
// field. It accepts a bare string (~91% of cases) or an array of
// sub-blocks (~9%). A bare string becomes one BlockText.
func decodeToolResultContent(raw json.RawMessage) ([]ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := trimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []ContentBlock{{Type: BlockText, Text: s}}, nil
	case '[':
		return decodeBlocks(raw)
	case 'n':
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected tool_result.content shape")
	}
}

// assistantEvents handles type:"assistant" lines. One JSONL line maps
// to one EventAssistant followed by one EventToolUse per tool_use
// content block in observation order. ToolUse events share the
// assistant line's UUID/ParentUUID/Timestamp; the docstring on Events
// describes the canonical ordering.
func assistantEvents(el *eventLine, sessionID string) ([]Event, error) {
	var msg rawAssistantMessage
	if len(el.Message) > 0 {
		if err := json.Unmarshal(el.Message, &msg); err != nil {
			return nil, fmt.Errorf("assistant.message: %w", err)
		}
	}
	if msg.IsMeta != nil && *msg.IsMeta {
		return nil, nil
	}

	blocks, err := decodeBlocks(msg.Content)
	if err != nil {
		return nil, fmt.Errorf("assistant.message.content: %w", err)
	}

	ts, _ := parseTimestamp(el.Timestamp)

	am := &AssistantMessage{
		Model:      msg.Model,
		MessageID:  msg.ID,
		RequestID:  el.RequestID,
		Text:       flattenTextBlocks(blocks),
		StopReason: msg.StopReason,
	}
	if msg.Usage != nil {
		am.Tokens = TokenStats{
			Input:         msg.Usage.InputTokens,
			Output:        msg.Usage.OutputTokens,
			CacheRead:     msg.Usage.CacheReadInputTokens,
			CacheCreation: msg.Usage.CacheCreationInputTokens,
		}
	}

	out := []Event{{
		SessionID:  sessionID,
		UUID:       el.UUID,
		ParentUUID: el.ParentUUID,
		Timestamp:  ts,
		Kind:       EventAssistant,
		Assistant:  am,
	}}

	// Walk content blocks again (raw) so ToolUse events preserve the
	// original tool input as raw JSON.
	if len(msg.Content) > 0 {
		var rawBlocks []rawContentBlock
		if err := json.Unmarshal(msg.Content, &rawBlocks); err == nil {
			for _, rb := range rawBlocks {
				if rb.Type != "tool_use" {
					continue
				}
				// json.RawMessage is a byte slice into the line's
				// content. Copy so the caller can hold it past the
				// next scanner refill (the parent scan already copied
				// the line, but a defensive copy here keeps the
				// invariant local).
				input := append(json.RawMessage(nil), rb.Input...)
				out = append(out, Event{
					SessionID:  sessionID,
					UUID:       el.UUID,
					ParentUUID: el.ParentUUID,
					Timestamp:  ts,
					Kind:       EventToolUse,
					ToolUse: &ToolUse{
						ID:    rb.ID,
						Name:  rb.Name,
						Input: input,
					},
				})
			}
		}
	}

	return out, nil
}

// compactBoundaryEvents handles system+compact_boundary lines.
func compactBoundaryEvents(el *eventLine, sessionID string) ([]Event, error) {
	var meta rawCompactMetadata
	if len(el.CompactMetadata) > 0 {
		if err := json.Unmarshal(el.CompactMetadata, &meta); err != nil {
			return nil, fmt.Errorf("compactMetadata: %w", err)
		}
	}
	ts, _ := parseTimestamp(el.Timestamp)
	return []Event{{
		SessionID:  sessionID,
		UUID:       el.UUID,
		ParentUUID: el.ParentUUID,
		Timestamp:  ts,
		Kind:       EventCompactBoundary,
		CompactBoundary: &CompactBoundary{
			PreCompactTokens:  meta.PreTokens,
			PostCompactTokens: meta.PostTokens,
			Trigger:           meta.Trigger,
		},
	}}, nil
}

// decodeBlocks turns a raw JSON array of content blocks into a typed
// []ContentBlock. Unknown block types are skipped silently (forward
// compatibility — the schema doc notes Anthropic adds types
// additively). tool_use and tool_result blocks are not represented in
// the ContentBlock union; they surface as Event kinds at the line
// level.
func decodeBlocks(raw json.RawMessage) ([]ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := trimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, nil
	}
	var rawBlocks []rawContentBlock
	if err := json.Unmarshal(raw, &rawBlocks); err != nil {
		return nil, err
	}
	var out []ContentBlock
	for _, rb := range rawBlocks {
		switch rb.Type {
		case "text":
			out = append(out, ContentBlock{Type: BlockText, Text: rb.Text})
		case "tool_reference":
			out = append(out, ContentBlock{
				Type:          BlockToolReference,
				ToolReference: &ToolReference{ToolName: rb.ToolName},
			})
		case "image":
			ib := &ImageBlock{}
			if rb.Source != nil {
				ib.MediaType = rb.Source.MediaType
				ib.Data = rb.Source.Data
			}
			out = append(out, ContentBlock{Type: BlockImage, Image: ib})
		default:
			// thinking, tool_use, tool_result, etc. — skipped.
		}
	}
	return out, nil
}

// flattenTextBlocks concatenates all BlockText.Text values with "\n"
// between them. Returns "" for an empty or all-non-text input.
func flattenTextBlocks(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == BlockText {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// trimSpace returns raw without leading ASCII whitespace. Used by
// decoders that peek at the first non-whitespace byte to dispatch on
// JSON value type (string vs array vs null).
func trimSpace(raw json.RawMessage) json.RawMessage {
	i := 0
	for i < len(raw) {
		b := raw[i]
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			break
		}
		i++
	}
	return raw[i:]
}
