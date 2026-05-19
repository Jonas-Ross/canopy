package sessions

import (
	"bufio"
	"bytes"
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
// permanently bloat every pooled buffer.
const retainedScanBufCap = 256 * 1024

// scanBufPool reuses bufio.Scanner backing arrays across scanFileMeta
// calls. Without it, Open allocates a fresh 64 KB buffer per JSONL file
// (532 × 64 KB ≈ 34 MB per Open on the user's tree).
var scanBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, initScanBufSize)
		return &b
	},
}

// Canonical type-tag bytes used to short-circuit non-conversation lines
// before any field-level scanning. Relies on the Claude CLI's canonical
// JSON output (no whitespace, type-first). A non-canonical conv line
// would be missed; covered by TestScanFileMeta_NonCanonicalKeyOrder
// which exercises a line where "type" is NOT the first key.
var (
	typeTagUser      = []byte(`"type":"user"`)
	typeTagAssistant = []byte(`"type":"assistant"`)
)

// Canonical field-search patterns for jsonStringField. Hoisted to
// package scope so the byte slice itself is allocated once per process.
var (
	typePat      = []byte(`"type":"`)
	uuidPat      = []byte(`"uuid":"`)
	timestampPat = []byte(`"timestamp":"`)
	cwdPat       = []byte(`"cwd":"`)
	modelPat     = []byte(`"model":"`)

	userTypeBytes      = []byte("user")
	assistantTypeBytes = []byte("assistant")
	syntheticBytes     = []byte("<synthetic>")
)

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
// Malformed lines are tolerated: a line that lacks a required field is
// skipped rather than aborting the file. Lines longer than
// maxScanLineSize cause the scan to error out with the bufio sentinel;
// callers wrap that with the file path.
//
// Hot path: no json.Unmarshal. Direct bytes.Index extraction is safe
// here because the Claude CLI emits canonical JSON — interior " in any
// nested string value is escaped to \", so the raw byte sequence
// `"key":"` only appears at the object's top level (or inside the
// message sub-object in the case of `"model":"`, which is uniquely
// scoped to assistant envelopes per docs/jsonl-schema.md §3-4).
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

	// Hold first/last fields as byte slices in caller-owned scratch
	// buffers. Without this, every conv line would allocate a string
	// just to overwrite lastCwd on the next iteration.
	var firstCwdBuf, firstTimestampBuf, lastCwdBuf []byte
	gotFirst := false
	modelCaptured := false

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Fast path: skip lines that cannot be conversation events
		// without any further parsing. The vast majority of lines in
		// real ~/.claude/projects files are meta / side-band
		// (queue-operation, system, attachment, file-history-snapshot,
		// permission-mode, …) and fall here.
		if !bytes.Contains(line, typeTagUser) && !bytes.Contains(line, typeTagAssistant) {
			continue
		}

		typeBytes, ok := jsonStringField(line, typePat)
		if !ok {
			continue
		}
		isUser := bytes.Equal(typeBytes, userTypeBytes)
		isAssistant := bytes.Equal(typeBytes, assistantTypeBytes)
		if !isUser && !isAssistant {
			// Pre-filter false positive (e.g. a substring match on a
			// non-conv line whose payload literally contained
			// `"type":"user"` somewhere we couldn't detect with the
			// canonical-JSON assumption).
			continue
		}

		uuidBytes, ok := jsonStringField(line, uuidPat)
		if !ok || len(uuidBytes) == 0 {
			// Meta lines lack uuids (docs/jsonl-schema.md §3); skip.
			continue
		}

		// Capture the first non-<synthetic> assistant model. "model"
		// only appears at top-level inside the message sub-object,
		// which is unique to assistant lines — see scanFileMeta godoc.
		if !modelCaptured && isAssistant {
			if modelBytes, ok := jsonStringField(line, modelPat); ok && len(modelBytes) > 0 && !bytes.Equal(modelBytes, syntheticBytes) {
				m.model = string(modelBytes)
				modelCaptured = true
			}
		}

		timestampBytes, _ := jsonStringField(line, timestampPat)
		cwdBytes, _ := jsonStringField(line, cwdPat)

		if !gotFirst {
			firstCwdBuf = append(firstCwdBuf[:0], cwdBytes...)
			firstTimestampBuf = append(firstTimestampBuf[:0], timestampBytes...)
			gotFirst = true
		}
		lastCwdBuf = append(lastCwdBuf[:0], cwdBytes...)
	}
	if err := sc.Err(); err != nil {
		return m, fmt.Errorf("scan: %w", err)
	}

	if !gotFirst {
		return m, nil
	}

	m.hasAnyConvLine = true
	m.firstCwd = string(firstCwdBuf)
	m.lastCwd = string(lastCwdBuf)
	if t, err := parseTimestamp(string(firstTimestampBuf)); err == nil {
		m.startedAt = t
	}
	return m, nil
}

// jsonStringField extracts the value of a top-level string-typed field
// from a canonical JSONL line. pat must include the surrounding
// punctuation, e.g. `"uuid":"`. Returns (nil, false) when the pattern
// is absent or the value is unterminated.
//
// The returned slice aliases the input — callers that need to retain
// the value past the next bufio.Scanner.Scan must copy it.
//
// Does NOT honor JSON escape sequences inside the matched value. The
// fields scanFileMeta extracts (type, uuid, timestamp, cwd, model) are
// all enum-like or generated identifiers that never contain " or \ in
// CLI output, so a literal scan to the next " is safe. Adding a new
// caller for a field whose value can contain embedded quotes would
// require revisiting this.
func jsonStringField(line, pat []byte) ([]byte, bool) {
	i := bytes.Index(line, pat)
	if i < 0 {
		return nil, false
	}
	start := i + len(pat)
	j := bytes.IndexByte(line[start:], '"')
	if j < 0 {
		return nil, false
	}
	return line[start : start+j], true
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
