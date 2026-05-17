package sessions

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	// eventsTestdataRoot is the isolated fixture tree for Events()
	// coverage. Kept separate from the static testdata/projects root so
	// adding fixtures here doesn't perturb the shared count-based
	// assertions in store_test.go.
	eventsTestdataRoot = "testdata/events"

	idEvents             = "00000010-0000-0000-0000-000000000010"
	idMalformed          = "00000011-0000-0000-0000-000000000011"
	idAgentPair          = "00000020-0000-0000-0000-000000000020"
	idAgentPairSubagent  = "00000020-0000-0000-0000-000000000020#pp"
)

func openEventsStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(eventsTestdataRoot)
	if err != nil {
		t.Fatalf("Open(%q): %v", eventsTestdataRoot, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// collectEvents drains the iterator into two parallel slices (events,
// errors). Errors do not stop iteration, matching the Events contract.
func collectEvents(it func(yield func(Event, error) bool)) ([]Event, []error) {
	var evs []Event
	var errs []error
	for ev, err := range it {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		evs = append(evs, ev)
	}
	return evs, errs
}

func TestEvents_NotFoundYieldsSentinel(t *testing.T) {
	store := openEventsStore(t)

	count := 0
	var gotErr error
	var gotEv Event
	for ev, err := range store.Events("does-not-exist") {
		count++
		gotEv = ev
		gotErr = err
		// Don't break: the iterator must terminate on its own.
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 yield, got %d", count)
	}
	if gotErr == nil || !errors.Is(gotErr, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", gotErr)
	}
	if gotEv != (Event{}) {
		t.Errorf("expected zero Event with sentinel, got %+v", gotEv)
	}
}

func TestEvents_HappyPathOrder(t *testing.T) {
	store := openEventsStore(t)

	evs, errs := collectEvents(store.Events(idEvents))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	// Per the fixture:
	//   line 1: attachment            → skip
	//   line 2: user (plain string)   → EventUser
	//   line 3: user (array)          → EventUser
	//   line 4: assistant w/ tool_use → EventAssistant + EventToolUse
	//   line 5: user tool_result (string) → EventToolResult
	//   line 6: user tool_result (blocks) → EventToolResult
	//   line 7: system compact_boundary  → EventCompactBoundary
	//   line 8: system stop_hook_summary → skip
	//   line 9: user isMeta:true       → skip
	//   line 10: permission-mode       → skip
	//   line 11: ai-title              → skip
	//   line 12: assistant final       → EventAssistant
	wantKinds := []EventKind{
		EventUser,
		EventUser,
		EventAssistant,
		EventToolUse,
		EventToolResult,
		EventToolResult,
		EventCompactBoundary,
		EventAssistant,
	}
	if len(evs) != len(wantKinds) {
		t.Fatalf("event count: got %d, want %d (kinds: %v)", len(evs), len(wantKinds), kindsOf(evs))
	}
	for i, want := range wantKinds {
		if evs[i].Kind != want {
			t.Errorf("event[%d].Kind = %v, want %v", i, evs[i].Kind, want)
		}
		if evs[i].SessionID != idEvents {
			t.Errorf("event[%d].SessionID = %q, want %q", i, evs[i].SessionID, idEvents)
		}
	}

	// Spot-check UUIDs preserve file order.
	wantUUIDs := []string{
		"f0000010-0000-0000-0000-000000000002",
		"f0000010-0000-0000-0000-000000000003",
		"f0000010-0000-0000-0000-000000000004",
		"f0000010-0000-0000-0000-000000000004",
		"f0000010-0000-0000-0000-000000000005",
		"f0000010-0000-0000-0000-000000000006",
		"f0000010-0000-0000-0000-000000000007",
		"f0000010-0000-0000-0000-000000000010",
	}
	for i, want := range wantUUIDs {
		if evs[i].UUID != want {
			t.Errorf("event[%d].UUID = %q, want %q", i, evs[i].UUID, want)
		}
	}
}

func TestEvents_UserStringContent(t *testing.T) {
	ev := firstEventOfKind(t, idEvents, func(e Event) bool {
		return e.Kind == EventUser && e.UUID == "f0000010-0000-0000-0000-000000000002"
	})
	if ev.User == nil {
		t.Fatal("user payload is nil")
	}
	if ev.User.Text != "plain string prompt" {
		t.Errorf("Text=%q, want %q", ev.User.Text, "plain string prompt")
	}
	if len(ev.User.Content) != 1 {
		t.Fatalf("len(Content)=%d, want 1", len(ev.User.Content))
	}
	if ev.User.Content[0].Type != BlockText {
		t.Errorf("Content[0].Type=%v, want BlockText", ev.User.Content[0].Type)
	}
	if ev.User.Content[0].Text != "plain string prompt" {
		t.Errorf("Content[0].Text=%q, want %q", ev.User.Content[0].Text, "plain string prompt")
	}
	if ev.User.Cwd != "/tmp/projects/events" {
		t.Errorf("Cwd=%q, want /tmp/projects/events", ev.User.Cwd)
	}
	if ev.User.Version != "2.1.143" {
		t.Errorf("Version=%q, want 2.1.143", ev.User.Version)
	}
}

func TestEvents_UserArrayContent(t *testing.T) {
	ev := firstEventOfKind(t, idEvents, func(e Event) bool {
		return e.Kind == EventUser && e.UUID == "f0000010-0000-0000-0000-000000000003"
	})
	if ev.User == nil {
		t.Fatal("user payload nil")
	}
	if ev.User.Text != "hello" {
		t.Errorf("Text=%q, want %q", ev.User.Text, "hello")
	}
	if len(ev.User.Content) != 2 {
		t.Fatalf("len(Content)=%d, want 2 (text+image)", len(ev.User.Content))
	}
	if ev.User.Content[0].Type != BlockText || ev.User.Content[0].Text != "hello" {
		t.Errorf("Content[0]=%+v, want BlockText:hello", ev.User.Content[0])
	}
	if ev.User.Content[1].Type != BlockImage {
		t.Errorf("Content[1].Type=%v, want BlockImage", ev.User.Content[1].Type)
	}
	if ev.User.Content[1].Image == nil || ev.User.Content[1].Image.MediaType != "image/png" {
		t.Errorf("Image=%+v, want media_type=image/png", ev.User.Content[1].Image)
	}
}

func TestEvents_AssistantWithToolUse(t *testing.T) {
	store := openEventsStore(t)

	// Pull all events for the line with msg_a1 (UUID …0004): we expect
	// EventAssistant immediately followed by EventToolUse, both sharing
	// the same UUID and ParentUUID, in that order.
	var seq []Event
	for ev, err := range store.Events(idEvents) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ev.UUID == "f0000010-0000-0000-0000-000000000004" {
			seq = append(seq, ev)
		}
	}
	if len(seq) != 2 {
		t.Fatalf("want 2 events for the tool_use line, got %d", len(seq))
	}
	if seq[0].Kind != EventAssistant {
		t.Errorf("seq[0].Kind=%v, want EventAssistant", seq[0].Kind)
	}
	if seq[1].Kind != EventToolUse {
		t.Errorf("seq[1].Kind=%v, want EventToolUse", seq[1].Kind)
	}
	if seq[0].ParentUUID != seq[1].ParentUUID {
		t.Errorf("ParentUUID mismatch: %q vs %q", seq[0].ParentUUID, seq[1].ParentUUID)
	}

	// Assistant payload sanity.
	am := seq[0].Assistant
	if am == nil {
		t.Fatal("assistant payload nil")
	}
	if am.MessageID != "msg_a1" {
		t.Errorf("MessageID=%q, want msg_a1", am.MessageID)
	}
	if am.RequestID != "req_a1" {
		t.Errorf("RequestID=%q, want req_a1", am.RequestID)
	}
	if am.Model != "claude-opus-4-7" {
		t.Errorf("Model=%q, want claude-opus-4-7", am.Model)
	}
	if am.StopReason != "tool_use" {
		t.Errorf("StopReason=%q, want tool_use", am.StopReason)
	}
	if am.Text != "I will run ls." {
		t.Errorf("Text=%q, want %q", am.Text, "I will run ls.")
	}
	wantTok := TokenStats{Input: 12, Output: 7, CacheRead: 3, CacheCreation: 4}
	if am.Tokens != wantTok {
		t.Errorf("Tokens=%+v, want %+v", am.Tokens, wantTok)
	}

	// ToolUse payload.
	tu := seq[1].ToolUse
	if tu == nil {
		t.Fatal("tool_use payload nil")
	}
	if tu.ID != "tu_a1" {
		t.Errorf("ToolUse.ID=%q, want tu_a1", tu.ID)
	}
	if tu.Name != "Bash" {
		t.Errorf("ToolUse.Name=%q, want Bash", tu.Name)
	}
	// Input is opaque JSON; verify it parses and carries the expected
	// command. Use a Decoder to avoid sensitivity to whitespace.
	var got map[string]any
	if err := json.NewDecoder(bytes.NewReader(tu.Input)).Decode(&got); err != nil {
		t.Fatalf("ToolUse.Input parse: %v (raw=%s)", err, string(tu.Input))
	}
	if got["command"] != "ls" {
		t.Errorf("ToolUse.Input.command=%v, want ls", got["command"])
	}
}

func TestEvents_UserToolResultBecomesToolResultEvent(t *testing.T) {
	// Two tool_result lines exist in the fixture:
	//   ...0005 → tool_result with bare-string content (success)
	//   ...0006 → tool_result with array content (error)
	store := openEventsStore(t)

	var got []Event
	for ev, err := range store.Events(idEvents) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ev.Kind == EventToolResult {
			got = append(got, ev)
		}
		// Also assert no EventUser event leaked from those tool_result
		// lines.
		if ev.Kind == EventUser && (ev.UUID == "f0000010-0000-0000-0000-000000000005" ||
			ev.UUID == "f0000010-0000-0000-0000-000000000006") {
			t.Fatalf("tool_result line surfaced as EventUser (uuid=%s)", ev.UUID)
		}
	}
	if len(got) != 2 {
		t.Fatalf("len(toolResults)=%d, want 2", len(got))
	}

	// First: string-form content.
	r0 := got[0].ToolResult
	if r0 == nil {
		t.Fatal("ToolResult[0] payload nil")
	}
	if r0.ToolUseID != "tu_a1" {
		t.Errorf("[0].ToolUseID=%q, want tu_a1", r0.ToolUseID)
	}
	if r0.IsError {
		t.Errorf("[0].IsError=true, want false")
	}
	if len(r0.Content) != 1 || r0.Content[0].Type != BlockText {
		t.Fatalf("[0].Content=%+v, want single BlockText", r0.Content)
	}
	if r0.Content[0].Text != "file1\nfile2\n" {
		t.Errorf("[0].Content[0].Text=%q, want %q", r0.Content[0].Text, "file1\nfile2\n")
	}
	if got0Str := r0.String(); got0Str != "file1\nfile2\n" {
		t.Errorf("[0].String()=%q, want %q", got0Str, "file1\nfile2\n")
	}

	// Second: array-form content with text + tool_reference + image.
	r1 := got[1].ToolResult
	if r1 == nil {
		t.Fatal("ToolResult[1] payload nil")
	}
	if r1.ToolUseID != "tu_a2" {
		t.Errorf("[1].ToolUseID=%q, want tu_a2", r1.ToolUseID)
	}
	if !r1.IsError {
		t.Errorf("[1].IsError=false, want true")
	}
	if len(r1.Content) != 3 {
		t.Fatalf("[1].Content len=%d, want 3", len(r1.Content))
	}
	if r1.Content[0].Type != BlockText || r1.Content[0].Text != "diagnostic line" {
		t.Errorf("[1].Content[0]=%+v, want BlockText:diagnostic line", r1.Content[0])
	}
	if r1.Content[1].Type != BlockToolReference || r1.Content[1].ToolReference == nil ||
		r1.Content[1].ToolReference.ToolName != "Read" {
		t.Errorf("[1].Content[1]=%+v, want BlockToolReference:Read", r1.Content[1])
	}
	if r1.Content[2].Type != BlockImage {
		t.Errorf("[1].Content[2].Type=%v, want BlockImage", r1.Content[2].Type)
	}
	// String() flattens to text only.
	if got1Str := r1.String(); got1Str != "diagnostic line" {
		t.Errorf("[1].String()=%q, want %q", got1Str, "diagnostic line")
	}
}

func TestEvents_ToolResultContentBlockArray(t *testing.T) {
	// Covered structurally inside the previous test; this is a focused
	// alias kept here so the suite reads with the test-name list in
	// the M1.1b brief.
	store := openEventsStore(t)
	var arrayResult *ToolResult
	for ev, err := range store.Events(idEvents) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ev.Kind == EventToolResult && ev.UUID == "f0000010-0000-0000-0000-000000000006" {
			arrayResult = ev.ToolResult
		}
	}
	if arrayResult == nil {
		t.Fatal("array-form tool_result not found")
	}
	if len(arrayResult.Content) != 3 {
		t.Errorf("len(Content)=%d, want 3", len(arrayResult.Content))
	}
	// Non-text blocks must be dropped by String().
	if got := arrayResult.String(); got != "diagnostic line" {
		t.Errorf("String()=%q, want %q", got, "diagnostic line")
	}
}

func TestEvents_CompactBoundaryPayload(t *testing.T) {
	ev := firstEventOfKind(t, idEvents, func(e Event) bool {
		return e.Kind == EventCompactBoundary
	})
	if ev.CompactBoundary == nil {
		t.Fatal("CompactBoundary payload nil")
	}
	cb := ev.CompactBoundary
	if cb.PreCompactTokens != 12000 {
		t.Errorf("PreCompactTokens=%d, want 12000", cb.PreCompactTokens)
	}
	if cb.PostCompactTokens != 3000 {
		t.Errorf("PostCompactTokens=%d, want 3000", cb.PostCompactTokens)
	}
	if cb.Trigger != "auto" {
		t.Errorf("Trigger=%q, want auto", cb.Trigger)
	}
}

func TestEvents_DefaultHidesIsMeta(t *testing.T) {
	store := openEventsStore(t)
	for ev, err := range store.Events(idEvents) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		// The isMeta line in the fixture has UUID …0009.
		if ev.UUID == "f0000010-0000-0000-0000-000000000009" {
			t.Fatalf("isMeta:true line was emitted: %+v", ev)
		}
	}
}

func TestEvents_SkipsOtherTypes(t *testing.T) {
	// attachment, permission-mode, ai-title, stop_hook_summary (system
	// subtype not compact_boundary) all live in the fixture.
	store := openEventsStore(t)
	for ev, err := range store.Events(idEvents) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		// Attachment UUID …0001 must not appear.
		if ev.UUID == "f0000010-0000-0000-0000-000000000001" {
			t.Errorf("attachment line surfaced: %+v", ev)
		}
		// Non-compact_boundary system line UUID …0008 must not appear.
		if ev.UUID == "f0000010-0000-0000-0000-000000000008" {
			t.Errorf("non-boundary system line surfaced: %+v", ev)
		}
	}
}

func TestEvents_MalformedLineYieldsErrorAndContinues(t *testing.T) {
	store := openEventsStore(t)

	evs, errs := collectEvents(store.Events(idMalformed))
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "malformed line 2") {
		t.Errorf("error doesn't mention line 2: %v", errs[0])
	}
	if !strings.Contains(errs[0].Error(), "00000011-0000-0000-0000-000000000011.jsonl") {
		t.Errorf("error doesn't mention file path: %v", errs[0])
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 valid events (user+assistant), got %d", len(evs))
	}
	if evs[0].Kind != EventUser {
		t.Errorf("evs[0].Kind=%v, want EventUser", evs[0].Kind)
	}
	if evs[1].Kind != EventAssistant {
		t.Errorf("evs[1].Kind=%v, want EventAssistant", evs[1].Kind)
	}
}

func TestEvents_SubagentEventsCarryCompositeSessionID(t *testing.T) {
	store := openEventsStore(t)

	saw := 0
	for ev, err := range store.Events(idAgentPairSubagent) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		saw++
		if ev.SessionID != idAgentPairSubagent {
			t.Errorf("event.SessionID=%q, want %q", ev.SessionID, idAgentPairSubagent)
		}
	}
	if saw == 0 {
		t.Fatal("no events surfaced from subagent file")
	}

	// And the parent: events should carry the bare (non-composite) ID.
	parentSaw := 0
	for ev, err := range store.Events(idAgentPair) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		parentSaw++
		if ev.SessionID != idAgentPair {
			t.Errorf("parent event.SessionID=%q, want %q", ev.SessionID, idAgentPair)
		}
		if strings.Contains(ev.SessionID, "#") {
			t.Errorf("parent event.SessionID has #: %q", ev.SessionID)
		}
	}
	if parentSaw == 0 {
		t.Fatal("no events surfaced from parent agent file")
	}
}

// --- helpers ---

// firstEventOfKind iterates store.Events(sessID) and returns the first
// event matching pred. Fails the test if no such event is found.
func firstEventOfKind(t *testing.T, sessID string, pred func(Event) bool) Event {
	t.Helper()
	store := openEventsStore(t)
	for ev, err := range store.Events(sessID) {
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if pred(ev) {
			return ev
		}
	}
	t.Fatalf("no event matched predicate in session %q", sessID)
	return Event{}
}

func kindsOf(evs []Event) []EventKind {
	out := make([]EventKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}
