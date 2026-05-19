package sessions

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestScanFileMeta_FirstAndLastCwd pins that cwd from the first and last
// conversation events is what populates extractedMeta, and that meta /
// side-band lines in between are ignored for cwd purposes.
func TestScanFileMeta_FirstAndLastCwd(t *testing.T) {
	body := joinLines(
		// Meta line (no uuid) — must NOT influence cwd.
		`{"type":"queue-operation","operation":"enqueue","cwd":"/should/be/ignored"}`,
		// First conversation event: user with cwd A.
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/path/a","message":{"role":"user","content":"hi"}}`,
		// Side-band line (has uuid, but type=system) — must NOT influence cwd.
		`{"type":"system","uuid":"s1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/should/be/ignored"}`,
		// Last conversation event: assistant with cwd B.
		`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:02.000Z","cwd":"/path/b","message":{"model":"claude-opus-4-7","content":[]}}`,
	)

	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if !got.hasAnyConvLine {
		t.Fatalf("hasAnyConvLine = false, want true")
	}
	if got.firstCwd != "/path/a" {
		t.Errorf("firstCwd = %q, want /path/a", got.firstCwd)
	}
	if got.lastCwd != "/path/b" {
		t.Errorf("lastCwd = %q, want /path/b", got.lastCwd)
	}
}

// TestScanFileMeta_StartedAt pins that startedAt is taken from the FIRST
// conversation event's timestamp (not last, not any meta line).
func TestScanFileMeta_StartedAt(t *testing.T) {
	body := joinLines(
		`{"type":"user","uuid":"u1","timestamp":"2026-03-15T08:30:45.123Z","cwd":"/x","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-03-15T08:30:46.999Z","cwd":"/x","message":{"model":"claude-opus-4-7","content":[]}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-03-15T08:30:45.123Z")
	if !got.startedAt.Equal(want) {
		t.Errorf("startedAt = %v, want %v", got.startedAt, want)
	}
}

// TestScanFileMeta_ModelExtraction pins that:
//   - the first non-"<synthetic>" assistant model wins,
//   - synthetic models are skipped over until a real one is seen,
//   - models on non-assistant lines are ignored.
func TestScanFileMeta_ModelExtraction(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "first non-synthetic wins",
			body: joinLines(
				`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/x","message":{"role":"user","content":"hi"}}`,
				`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/x","message":{"model":"claude-opus-4-7","content":[]}}`,
				`{"type":"assistant","uuid":"a2","timestamp":"2026-01-01T00:00:02.000Z","cwd":"/x","message":{"model":"claude-sonnet-4-6","content":[]}}`,
			),
			want: "claude-opus-4-7",
		},
		{
			name: "synthetic skipped, real one wins",
			body: joinLines(
				`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/x","message":{"model":"<synthetic>","content":[]}}`,
				`{"type":"assistant","uuid":"a2","timestamp":"2026-01-01T00:00:02.000Z","cwd":"/x","message":{"model":"<synthetic>","content":[]}}`,
				`{"type":"assistant","uuid":"a3","timestamp":"2026-01-01T00:00:03.000Z","cwd":"/x","message":{"model":"claude-haiku-4-5","content":[]}}`,
			),
			want: "claude-haiku-4-5",
		},
		{
			name: "user-level model field ignored",
			body: joinLines(
				`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/x","message":{"role":"user","model":"should-not-win","content":"hi"}}`,
				`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/x","message":{"model":"claude-opus-4-7","content":[]}}`,
			),
			want: "claude-opus-4-7",
		},
		{
			name: "all synthetic leaves model empty",
			body: joinLines(
				`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/x","message":{"model":"<synthetic>","content":[]}}`,
			),
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scanFileMeta(strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("scanFileMeta: %v", err)
			}
			if got.model != tc.want {
				t.Errorf("model = %q, want %q", got.model, tc.want)
			}
		})
	}
}

// TestScanFileMeta_MetaLinesIgnored pins that lines without a uuid are
// treated as non-conversation regardless of type and never affect the
// first/last cwd or startedAt.
func TestScanFileMeta_MetaLinesIgnored(t *testing.T) {
	body := joinLines(
		// All meta types from docs/jsonl-schema.md §3 that lack a uuid.
		`{"type":"permission-mode","mode":"acceptEdits"}`,
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"ai-title","title":"foo"}`,
		`{"type":"last-prompt","prompt":"hi"}`,
		`{"type":"pr-link","url":"https://example.com"}`,
		`{"type":"file-history-snapshot","files":[]}`,
		`{"type":"agent-name","name":"foo"}`,
		`{"type":"custom-title","title":"bar"}`,
		`{"type":"worktree-state","state":"clean"}`,
		`{"type":"agent-setting","key":"x"}`,
		// One genuine conv line so we can verify the extractedMeta is correct.
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/real","message":{"role":"user","content":"hi"}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if !got.hasAnyConvLine {
		t.Fatalf("hasAnyConvLine = false, want true")
	}
	if got.firstCwd != "/real" || got.lastCwd != "/real" {
		t.Errorf("cwds = (%q, %q), want both /real", got.firstCwd, got.lastCwd)
	}
}

// TestScanFileMeta_SidebandLinesSkipped pins that uuid-bearing lines
// with side-band types (system, attachment, file-history-snapshot) are
// NOT counted as conversation events even though they look conv-like.
func TestScanFileMeta_SidebandLinesSkipped(t *testing.T) {
	body := joinLines(
		`{"type":"system","uuid":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/not-conv"}`,
		`{"type":"attachment","uuid":"at1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/not-conv"}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:02.000Z","cwd":"/real","message":{"role":"user","content":"hi"}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.firstCwd != "/real" {
		t.Errorf("firstCwd = %q, want /real", got.firstCwd)
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-01-01T00:00:02.000Z")
	if !got.startedAt.Equal(want) {
		t.Errorf("startedAt = %v, want %v", got.startedAt, want)
	}
}

// TestScanFileMeta_MalformedToleratedAndConvFound pins that a malformed
// JSON line doesn't poison the file: subsequent valid conv lines still
// get indexed.
func TestScanFileMeta_MalformedTolerated(t *testing.T) {
	body := joinLines(
		`{this is not json`,
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/ok","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/ok","message":{"model":"claude-opus-4-7","content":[]}}`,
		`}}also not json{{`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if !got.hasAnyConvLine {
		t.Errorf("hasAnyConvLine = false, want true")
	}
	if got.firstCwd != "/ok" || got.lastCwd != "/ok" {
		t.Errorf("cwds = (%q, %q), want both /ok", got.firstCwd, got.lastCwd)
	}
	if got.model != "claude-opus-4-7" {
		t.Errorf("model = %q, want claude-opus-4-7", got.model)
	}
}

// TestScanFileMeta_EmptyFile pins that an empty reader returns a
// zero-value extractedMeta with hasAnyConvLine=false and no error.
func TestScanFileMeta_EmptyFile(t *testing.T) {
	got, err := scanFileMeta(strings.NewReader(""))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.hasAnyConvLine {
		t.Errorf("hasAnyConvLine = true, want false")
	}
}

// TestScanFileMeta_MetaOnlyFile pins that a file containing only meta /
// side-band lines (no conv events) is treated as empty: no error,
// hasAnyConvLine=false.
func TestScanFileMeta_MetaOnlyFile(t *testing.T) {
	body := joinLines(
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"permission-mode","mode":"plan"}`,
		`{"type":"system","uuid":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/x"}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.hasAnyConvLine {
		t.Errorf("hasAnyConvLine = true, want false")
	}
}

// TestScanFileMeta_VeryLargeLine pins that a multi-MB conv line is
// consumed without error (Scanner.Buffer ceiling honored).
func TestScanFileMeta_VeryLargeLine(t *testing.T) {
	// 2 MB of filler inside the user message content.
	filler := strings.Repeat("x", 2*1024*1024)
	body := joinLines(
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/big","message":{"role":"user","content":"` + filler + `"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/big","message":{"model":"claude-opus-4-7","content":[]}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.firstCwd != "/big" || got.lastCwd != "/big" {
		t.Errorf("cwds = (%q, %q), want both /big", got.firstCwd, got.lastCwd)
	}
	if got.model != "claude-opus-4-7" {
		t.Errorf("model = %q, want claude-opus-4-7", got.model)
	}
}

// TestScanFileMeta_NonCanonicalKeyOrder pins that scanFileMeta still
// extracts the right fields when keys appear in a non-canonical order
// (uuid before type, etc.). This is the safety net for the byte
// pre-filter optimization in Phase 2c — if the pre-filter is ever
// tightened too aggressively, this test fails.
func TestScanFileMeta_NonCanonicalKeyOrder(t *testing.T) {
	// Keys deliberately reordered: uuid, timestamp, cwd, type, message.
	body := joinLines(
		`{"uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/reorder","type":"user","message":{"role":"user","content":"hi"}}`,
		`{"uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/reorder","type":"assistant","message":{"model":"claude-opus-4-7","content":[]}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.firstCwd != "/reorder" || got.lastCwd != "/reorder" {
		t.Errorf("cwds = (%q, %q), want both /reorder", got.firstCwd, got.lastCwd)
	}
	if got.model != "claude-opus-4-7" {
		t.Errorf("model = %q, want claude-opus-4-7", got.model)
	}
}

// TestScanFileMeta_PooledBufferNoCrossContamination pins that running
// scanFileMeta twice in sequence over different inputs produces
// independent results — i.e. that any future scanner-buffer pooling
// does not leak bytes from one call into the next.
func TestScanFileMeta_PooledBufferNoCrossContamination(t *testing.T) {
	bodyA := joinLines(
		`{"type":"user","uuid":"uA","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/a","message":{"role":"user","content":"a"}}`,
	)
	bodyB := joinLines(
		`{"type":"user","uuid":"uB","timestamp":"2026-02-01T00:00:00.000Z","cwd":"/b","message":{"role":"user","content":"b"}}`,
	)

	gotA, err := scanFileMeta(strings.NewReader(bodyA))
	if err != nil {
		t.Fatalf("scanFileMeta A: %v", err)
	}
	gotB, err := scanFileMeta(bytes.NewReader([]byte(bodyB)))
	if err != nil {
		t.Fatalf("scanFileMeta B: %v", err)
	}
	if gotA.firstCwd != "/a" {
		t.Errorf("A firstCwd = %q, want /a", gotA.firstCwd)
	}
	if gotB.firstCwd != "/b" {
		t.Errorf("B firstCwd = %q, want /b", gotB.firstCwd)
	}
}

// TestScanFileMeta_ParentUuidDoesNotMaskUuid pins that the byte-level
// uuid extractor doesn't accidentally pick up "parentUuid" as the
// line's uuid. "parentUuid" precedes "uuid" alphabetically and the
// canonical CLI output places it FIRST on most lines, so a naive
// substring search for "uuid":" against a parentUuid'd line would
// otherwise extract the parent's uuid.
func TestScanFileMeta_ParentUuidDoesNotMaskUuid(t *testing.T) {
	body := joinLines(
		`{"type":"user","parentUuid":"pppp-1111","uuid":"uuuu-2222","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/x","message":{"role":"user","content":"hi"}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if !got.hasAnyConvLine {
		t.Fatalf("hasAnyConvLine = false, want true")
	}
	if got.firstCwd != "/x" {
		t.Errorf("firstCwd = %q, want /x", got.firstCwd)
	}
}

// TestScanFileMeta_AlphabetizedKeysWithNestedType pins that an
// assistant line serialized by Go's json.Encoder (which alphabetizes
// keys, so nested content-block `"type":"text"` literally precedes the
// top-level `"type":"assistant"` in the byte stream) is still
// classified as assistant and yields the model from the message
// envelope. internal/demo's fixture uses json.Encoder, so this is the
// shape that broke without the pre-filter-as-classifier rule.
func TestScanFileMeta_AlphabetizedKeysWithNestedType(t *testing.T) {
	body := joinLines(
		// Alphabetized order: cwd, message, sessionId, timestamp, type, uuid.
		// Crucially, message.content holds a content block with
		// "type":"text" — that nested `"type":"` appears in the byte
		// stream BEFORE the top-level `"type":"assistant"`.
		`{"cwd":"/demo","message":{"content":[{"text":"hi","type":"text"}],"id":"msg_01","model":"claude-opus-4-7","role":"assistant"},"sessionId":"s","timestamp":"2026-01-01T00:00:00.000Z","type":"assistant","uuid":"a1"}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if !got.hasAnyConvLine {
		t.Fatalf("hasAnyConvLine = false, want true (classified as non-conv?)")
	}
	if got.firstCwd != "/demo" {
		t.Errorf("firstCwd = %q, want /demo", got.firstCwd)
	}
	if got.model != "claude-opus-4-7" {
		t.Errorf("model = %q, want claude-opus-4-7", got.model)
	}
}

// TestScanFileMeta_EscapedQuotesInContent pins that a tool-result whose
// content payload contains the LITERAL bytes `"uuid":"..."` (after
// JSON-encoding, those `"` become `\"`) doesn't trick the extractor
// into reading a fake uuid. This is the safety net behind the "raw
// `"key":"` only appears at top level in canonical JSON" assumption.
func TestScanFileMeta_EscapedQuotesInContent(t *testing.T) {
	body := joinLines(
		// Real uuid is "real-uuid". The tool_result content contains a
		// JSON-encoded snippet whose interior quotes are escaped — those
		// must NOT short-circuit the real uuid extraction.
		`{"type":"user","uuid":"real-uuid","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/correct","message":{"role":"user","content":"{\"uuid\":\"fake-uuid\",\"cwd\":\"/wrong\",\"type\":\"user\"}"}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.firstCwd != "/correct" {
		t.Errorf("firstCwd = %q, want /correct (extractor confused by escaped content)", got.firstCwd)
	}
}

// TestScanFileMeta_EmptyCwdAllowed pins that an empty cwd value
// (`"cwd":""`) is preserved as-is rather than crashing the extractor.
// dedupeCwds downstream collapses this to nil; the field-level
// extractor just needs to round-trip the value faithfully.
func TestScanFileMeta_EmptyCwdAllowed(t *testing.T) {
	body := joinLines(
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"","message":{"role":"user","content":"hi"}}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if !got.hasAnyConvLine {
		t.Fatalf("hasAnyConvLine = false, want true")
	}
	if got.firstCwd != "" || got.lastCwd != "" {
		t.Errorf("cwds = (%q, %q), want both empty", got.firstCwd, got.lastCwd)
	}
}

// TestJsonStringField directly exercises the byte extractor used by
// scanFileMeta. Failures here narrow blame for any scanFileMeta
// regression introduced by extractor edge cases.
func TestJsonStringField(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		pat     string
		want    string
		wantOK  bool
	}{
		{"present", `{"type":"user"}`, `"type":"`, "user", true},
		{"absent", `{"type":"system"}`, `"missing":"`, "", false},
		{"empty value", `{"cwd":""}`, `"cwd":"`, "", true},
		{"value at end of object", `{"type":"assistant","model":"opus"}`, `"model":"`, "opus", true},
		{"unterminated", `{"type":"user`, `"type":"`, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := jsonStringField([]byte(tc.line), []byte(tc.pat))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if string(got) != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
		})
	}
}

// joinLines stitches JSONL test fixtures with newlines, leaving a final
// newline so bufio.Scanner sees the last line cleanly.
func joinLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}
