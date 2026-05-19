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

// Some real JSONL lines (attachments, large tool inputs/outputs) are
// multi-MB; this is the per-line ceiling for bufio.Scanner.
const maxScanLineSize = 10 * 1024 * 1024

const (
	initScanBufSize    = 64 * 1024
	retainedScanBufCap = 256 * 1024 // cap on what we keep in scanBufPool
)

var scanBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, initScanBufSize)
		return &b
	},
}

// Type detection uses the value-included typeTag* patterns, NOT
// jsonStringField — content blocks have their own `"type":"text"` /
// `"type":"tool_use"` keys that would shadow the top-level type once
// json.Encoder alphabetizes the line.
var (
	typeTagUser      = []byte(`"type":"user"`)
	typeTagAssistant = []byte(`"type":"assistant"`)
	uuidPat          = []byte(`"uuid":"`)
	timestampPat     = []byte(`"timestamp":"`)
	cwdPat           = []byte(`"cwd":"`)
	modelPat         = []byte(`"model":"`)
	syntheticBytes   = []byte("<synthetic>")
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
// "<synthetic>" assistant model. Malformed lines and lines missing the
// required fields are silently skipped; bufio.Scanner's ErrTooLong (a
// line larger than maxScanLineSize) is the only failure surfaced.
//
// Hot-path extraction skips json.Unmarshal in favor of direct byte
// scans. Safe because the CLI emits canonical JSON, so any `"key":"`
// substring inside a nested string value is escape-rewritten as
// `\"key\":\"` and cannot collide with the top-level keys we read.
func scanFileMeta(r io.Reader) (extractedMeta, error) {
	var m extractedMeta

	bufPtr := scanBufPool.Get().(*[]byte)
	defer func() {
		buf := (*bufPtr)[:0]
		if cap(buf) > retainedScanBufCap {
			// Outlier multi-MB file — drop the grown buffer so it doesn't
			// inflate every subsequent borrower.
			buf = make([]byte, 0, initScanBufSize)
		}
		*bufPtr = buf
		scanBufPool.Put(bufPtr)
	}()

	sc := bufio.NewScanner(r)
	sc.Buffer(*bufPtr, maxScanLineSize)

	// Byte scratch for first/last fields — avoids a string alloc per
	// conv line just to overwrite lastCwd on the next iteration.
	var firstCwdBuf, firstTimestampBuf, lastCwdBuf []byte
	gotFirst := false

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// A complete canonical JSONL line ends with '}'; a trailing
		// partial token (Scanner emits one when EOF lands mid-line)
		// does not. Reject it the way the old json.Unmarshal path did.
		if line[len(line)-1] != '}' {
			continue
		}
		isAssistant := bytes.Contains(line, typeTagAssistant)
		isUser := !isAssistant && bytes.Contains(line, typeTagUser)
		if !isUser && !isAssistant {
			continue
		}

		uuidBytes, ok := jsonStringField(line, uuidPat)
		if !ok || len(uuidBytes) == 0 {
			// Meta lines lack uuids (docs/jsonl-schema.md §3).
			continue
		}

		if m.model == "" && isAssistant {
			if modelBytes, ok := messageModelField(line); ok && len(modelBytes) > 0 && !bytes.Equal(modelBytes, syntheticBytes) {
				m.model = string(modelBytes)
			}
		}

		cwdBytes, _ := jsonStringField(line, cwdPat)

		if !gotFirst {
			timestampBytes, _ := jsonStringField(line, timestampPat)
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

// jsonStringField returns the string value following pat (which must
// include the trailing `":"`, e.g. `"uuid":"`) in a canonical JSONL
// line. The result aliases line; copy it before the next Scanner fill.
//
// Does not honor JSON escape sequences inside the matched value. The
// fields scanFileMeta extracts (uuid, timestamp, cwd) never contain "
// or \ in CLI output. A new caller whose value can carry embedded
// quotes must use a real JSON parse instead.
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

// messageModelField returns the value of the "model" string at the top
// level of the line's "message" object. A nested "model" key inside a
// content block (e.g. an LLM-wrapper tool's input) is ignored — only
// direct children of message match.
func messageModelField(line []byte) ([]byte, bool) {
	msgOpen := []byte(`"message":{`)
	start := bytes.Index(line, msgOpen)
	if start < 0 {
		return nil, false
	}
	i := start + len(msgOpen)
	n := len(line)
	depth := 0
	inString := false
	for i < n {
		c := line[i]
		if inString {
			if c == '\\' && i+1 < n {
				i += 2
				continue
			}
			if c == '"' {
				inString = false
			}
			i++
			continue
		}
		switch c {
		case '"':
			if depth == 0 && i+len(modelPat) <= n && bytes.Equal(line[i:i+len(modelPat)], modelPat) {
				valStart := i + len(modelPat)
				valEnd := valStart
				for valEnd < n {
					if line[valEnd] == '\\' && valEnd+1 < n {
						valEnd += 2
						continue
					}
					if line[valEnd] == '"' {
						return line[valStart:valEnd], true
					}
					valEnd++
				}
				return nil, false
			}
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return nil, false
			}
			depth--
		}
		i++
	}
	return nil, false
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
