package sessions

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestScanFileMeta_FirstAndLastCwd(t *testing.T) {
	body := joinLines(
		`{"type":"queue-operation","operation":"enqueue","cwd":"/should/be/ignored"}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/path/a","message":{"role":"user","content":"hi"}}`,
		`{"type":"system","uuid":"s1","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/should/be/ignored"}`,
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

// TestScanFileMeta_MetaLinesIgnored covers every meta type from
// docs/jsonl-schema.md §3 (none have uuid). One conv line at the end
// anchors the positive assertion.
func TestScanFileMeta_MetaLinesIgnored(t *testing.T) {
	body := joinLines(
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

func TestScanFileMeta_EmptyFile(t *testing.T) {
	got, err := scanFileMeta(strings.NewReader(""))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.hasAnyConvLine {
		t.Errorf("hasAnyConvLine = true, want false")
	}
}

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

func TestScanFileMeta_VeryLargeLine(t *testing.T) {
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

// TestScanFileMeta_NonCanonicalKeyOrder is the safety net for the byte
// pre-filter: if it ever tightens to require type-first, this fails.
func TestScanFileMeta_NonCanonicalKeyOrder(t *testing.T) {
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

// TestScanFileMeta_PooledBufferNoCrossContamination pins that the
// sync.Pool'd scanner buffer doesn't leak bytes from one call into the
// next.
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

// TestScanFileMeta_ParentUuidDoesNotMaskUuid pins that "parentUuid"
// (which appears first in canonical CLI output) doesn't shadow the
// real top-level "uuid" in jsonStringField.
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

// TestScanFileMeta_AlphabetizedKeysWithNestedType covers the shape Go's
// json.Encoder emits (alphabetized keys, so a nested content block's
// `"type":"text"` precedes the top-level `"type":"assistant"`).
// internal/demo writes fixtures this way.
func TestScanFileMeta_AlphabetizedKeysWithNestedType(t *testing.T) {
	body := joinLines(
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

// TestScanFileMeta_EscapedQuotesInContent is the safety net for the
// "raw `"key":"` only appears at top level in canonical JSON"
// assumption: a content payload that JSON-encodes `"uuid":"fake"`
// (so its quotes become `\"`) must not shadow the real uuid.
func TestScanFileMeta_EscapedQuotesInContent(t *testing.T) {
	body := joinLines(
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

// TestScanFileMeta_NestedToolModelIgnored guards against a tool_use
// input that carries its own "model" key (e.g. a wrapper tool taking a
// model name as argument). Alphabetized output puts message.content
// before message.model, so a naive first-occurrence scan would grab
// the tool's model instead of the assistant's.
func TestScanFileMeta_NestedToolModelIgnored(t *testing.T) {
	body := joinLines(
		`{"cwd":"/x","message":{"content":[{"type":"tool_use","name":"InvokeLLM","input":{"model":"tool-model-should-lose","prompt":"hi"}}],"id":"msg_01","model":"claude-opus-4-7","role":"assistant"},"sessionId":"s","timestamp":"2026-01-01T00:00:00.000Z","type":"assistant","uuid":"a1"}`,
	)
	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.model != "claude-opus-4-7" {
		t.Errorf("model = %q, want claude-opus-4-7 (nested tool model shadowed envelope?)", got.model)
	}
}

// TestScanFileMeta_PartialLineRejected guards against bufio.Scanner
// returning a trailing partial token when Open races a concurrent CLI
// write. The old json.Unmarshal path rejected such lines via parse
// error; the byte-extractor path must continue to do so.
func TestScanFileMeta_PartialLineRejected(t *testing.T) {
	// One complete conv line, then a partial conv line missing the
	// closing brace (and most of the message body).
	complete := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/complete","message":{"role":"user","content":"hi"}}`
	partial := `{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/partial","message":{"role":"user","content":"goodby`
	body := complete + "\n" + partial // no trailing newline — Scanner emits partial as a final token

	got, err := scanFileMeta(strings.NewReader(body))
	if err != nil {
		t.Fatalf("scanFileMeta: %v", err)
	}
	if got.firstCwd != "/complete" || got.lastCwd != "/complete" {
		t.Errorf("cwds = (%q, %q), want both /complete (partial line accepted?)", got.firstCwd, got.lastCwd)
	}
}

func joinLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}
