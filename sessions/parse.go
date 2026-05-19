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
	"sync"
	"time"
)

// maxScanLineSize is the buffer ceiling for bufio.Scanner. Some real
// JSONL lines (attachments, large tool inputs/outputs) are multi-MB.
const maxScanLineSize = 10 * 1024 * 1024

// initScanBufSize is the starting capacity for each pooled Scanner
// buffer. bufio.Scanner will grow past this when a single line exceeds
// the current buffer, up to maxScanLineSize.
const initScanBufSize = 64 * 1024

// retainedScanBufCap caps the capacity we keep when returning a buffer
// to scanBufPool. Without this, a single file with a multi-MB line would
// permanently bloat every pooled buffer. 256 KB is large enough to hold
// the vast majority of conversation lines without re-growing, and small
// enough that holding 18+ in the pool (one per cpu, worst case) stays
// well below the original 64 KB × 532-file baseline footprint.
const retainedScanBufCap = 256 * 1024

// scanBufPool reuses bufio.Scanner backing arrays across scanFileMeta
// calls. Without it, Open allocates a fresh 64 KB buffer per JSONL file
// (532 × 64 KB ≈ 34 MB per Open on the user's tree, plus the growth
// each time a multi-KB line forces an expand).
var scanBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, initScanBufSize)
		return &b
	},
}

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

// rawLine is the full projection of a JSONL line used until the first
// non-synthetic assistant model has been captured. Message is decoded
// as RawMessage so we can extract the model from the assistant
// envelope; after that point scanFileMeta switches to rawLineLean to
// skip the per-line RawMessage byte-copy.
type rawLine struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
}

// rawLineLean drops Message. Used after model capture; relies on
// json.Unmarshal silently ignoring unknown fields, which means the
// (potentially multi-MB) "message" payload is never copied into a
// json.RawMessage slice.
type rawLineLean struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

// rawMessage projects the assistant message envelope used for first
// non-synthetic model extraction.
type rawMessage struct {
	Model string `json:"model"`
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

	bufPtr := scanBufPool.Get().(*[]byte)
	defer func() {
		buf := (*bufPtr)[:0]
		if cap(buf) > retainedScanBufCap {
			// Don't keep multi-MB buffers in the pool — a single
			// outlier file would inflate every subsequent borrower.
			buf = make([]byte, 0, initScanBufSize)
		}
		*bufPtr = buf
		scanBufPool.Put(bufPtr)
	}()

	sc := bufio.NewScanner(r)
	sc.Buffer(*bufPtr, maxScanLineSize)

	var firstCwd, firstTimestamp, lastCwd string
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
		// the downstream type check still rejects it.
		if !bytes.Contains(line, typeTagUser) && !bytes.Contains(line, typeTagAssistant) {
			continue
		}

		var typeStr, uuidStr, timestampStr, cwdStr string

		if m.model == "" {
			// Full decode while we still need the assistant message
			// envelope to extract the first non-<synthetic> model.
			var raw rawLine
			if err := json.Unmarshal(line, &raw); err != nil {
				continue
			}
			if raw.Type == "assistant" && len(raw.Message) > 0 {
				var rm rawMessage
				if err := json.Unmarshal(raw.Message, &rm); err == nil {
					if rm.Model != "" && rm.Model != "<synthetic>" {
						m.model = rm.Model
					}
				}
			}
			typeStr, uuidStr, timestampStr, cwdStr = raw.Type, raw.UUID, raw.Timestamp, raw.Cwd
		} else {
			// Lean decode — `message` is no longer in the struct, so
			// json.Unmarshal skips its (potentially multi-MB) bytes
			// instead of copying them into a json.RawMessage.
			var raw rawLineLean
			if err := json.Unmarshal(line, &raw); err != nil {
				continue
			}
			typeStr, uuidStr, timestampStr, cwdStr = raw.Type, raw.UUID, raw.Timestamp, raw.Cwd
		}

		// Conversation event = uuid present (meta lines lack uuids per
		// docs/jsonl-schema.md §3) and type ∈ {user, assistant}.
		if uuidStr == "" || (typeStr != "user" && typeStr != "assistant") {
			continue
		}

		if !gotFirst {
			firstCwd = cwdStr
			firstTimestamp = timestampStr
			gotFirst = true
		}
		lastCwd = cwdStr
	}
	if err := sc.Err(); err != nil {
		return m, fmt.Errorf("scan: %w", err)
	}

	if !gotFirst {
		return m, nil
	}

	m.hasAnyConvLine = true
	m.firstCwd = firstCwd
	m.lastCwd = lastCwd
	if t, err := parseTimestamp(firstTimestamp); err == nil {
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
