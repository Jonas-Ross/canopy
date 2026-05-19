package sessions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// maxScanLineSize is the buffer ceiling for bufio.Scanner. Some real
// JSONL lines (attachments, large tool inputs/outputs) are multi-MB.
const maxScanLineSize = 10 * 1024 * 1024

// Canonical type-tag bytes used to short-circuit non-conversation lines
// before json.Unmarshal. Relies on the Claude CLI's canonical JSON
// output (no whitespace, type-first). A non-canonical line that
// otherwise represents a conversation event would be missed; this is
// covered by TestScanFileMeta_NonCanonicalKeyOrder, which exercises a
// line where "type" is NOT the first key.
var (
	typeTagUser      = []byte(`"type":"user"`)
	typeTagAssistant = []byte(`"type":"assistant"`)
)

// rawLine is a minimal projection of a JSONL line. Only fields needed
// for the Open-time first/last conversation event extraction are
// decoded. The rest of the JSON object is ignored.
type rawLine struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
}

// rawMessage projects the assistant message envelope used for first
// non-synthetic model extraction.
type rawMessage struct {
	Model string `json:"model"`
}

// isConversationEvent reports whether a raw line counts as a
// conversation event for indexing purposes. A line is a "conversation
// event" if its top-level type is "user" or "assistant" and it has a
// non-empty UUID (meta lines lack uuids per docs/jsonl-schema.md §3).
func isConversationEvent(r *rawLine) bool {
	if r.UUID == "" {
		return false
	}
	return r.Type == "user" || r.Type == "assistant"
}

// extractedMeta is what the Open-time scan pulls out of one JSONL file.
type extractedMeta struct {
	firstCwd       string
	lastCwd        string
	startedAt      time.Time
	model          string
	hasAnyConvLine bool
}

// scanFileMeta walks a single JSONL file and extracts the first and
// last conversation events' cwd + timestamp, plus the first non-
// "<synthetic>" assistant model. It does not keep all lines in memory.
//
// Malformed lines are tolerated: a JSON unmarshal error skips the line
// rather than aborting the file. Lines longer than maxScanLineSize
// cause the scan to error out with the bufio sentinel; callers wrap
// that with the file path.
func scanFileMeta(r io.Reader) (extractedMeta, error) {
	var m extractedMeta

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanLineSize)

	var first, last rawLine
	gotFirst := false

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Fast path: most JSONL lines are meta or side-band
		// (queue-operation, system, attachment, file-history-snapshot,
		// …). Skip the full json.Unmarshal — including the per-line
		// rawLine struct alloc and the json.RawMessage copy of
		// `message` — when the line cannot possibly be a conversation
		// event. Safe because the CLI emits canonical JSON; if a
		// non-conv line ever contains `"type":"user"` as a substring
		// (e.g. inside a stringified tool result), we fall through and
		// the downstream type check still rejects it. Falls back to
		// full parse for lines that look conv-ish.
		if !bytes.Contains(line, typeTagUser) && !bytes.Contains(line, typeTagAssistant) {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			// malformed line — skip; Open() is a best-effort indexer
			continue
		}

		// First non-synthetic assistant model wins.
		if m.model == "" && raw.Type == "assistant" && len(raw.Message) > 0 {
			var rm rawMessage
			if err := json.Unmarshal(raw.Message, &rm); err == nil {
				if rm.Model != "" && rm.Model != "<synthetic>" {
					m.model = rm.Model
				}
			}
		}

		if !isConversationEvent(&raw) {
			continue
		}

		if !gotFirst {
			first = raw
			gotFirst = true
		}
		last = raw
	}
	if err := sc.Err(); err != nil {
		return m, fmt.Errorf("scan: %w", err)
	}

	if !gotFirst {
		return m, nil
	}

	m.hasAnyConvLine = true
	m.firstCwd = first.Cwd
	m.lastCwd = last.Cwd
	if t, err := parseTimestamp(first.Timestamp); err == nil {
		m.startedAt = t
	}
	return m, nil
}

// parseTimestamp parses an RFC3339-with-millis timestamp as written by
// the CLI. Returns an error on empty input or unparseable timestamps.
func parseTimestamp(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("empty timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// readFileMeta is a convenience wrapper that opens the path and runs
// scanFileMeta. The caller owns wrapping any returned error with the
// file path.
func readFileMeta(path string) (extractedMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return extractedMeta{}, err
	}
	defer f.Close()
	return scanFileMeta(f)
}

// dedupeCwds collapses adjacent identical cwd entries. Used to compress
// first-and-last-cwd to a single entry when they match.
func dedupeCwds(first, last string) []string {
	if first == "" && last == "" {
		return nil
	}
	if first == last || last == "" {
		return []string{first}
	}
	if first == "" {
		return []string{last}
	}
	return []string{first, last}
}

// containsFoldPrefix reports whether s has a case-insensitive substring
// match of substr. Used by Query.Model substring matching.
func containsFoldSubstring(s, substr string) bool {
	if substr == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
