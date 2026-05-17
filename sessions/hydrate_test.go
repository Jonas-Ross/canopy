package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// writeHydrateFixture materializes a single-session JSONL fixture
// under a fresh TempDir laid out as Open expects:
// <root>/<projectDir>/<sessionId>.jsonl. Returns the root, sessionID,
// and the written file path.
func writeHydrateFixture(t *testing.T, projectDir, sessionID, content string) (root, id, path string) {
	t.Helper()
	root = t.TempDir()
	proj := filepath.Join(root, projectDir)
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(proj, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, sessionID, path
}

// openWith opens a Store at root with cleanup wired to t.Cleanup.
func openWith(t *testing.T, root string) *Store {
	t.Helper()
	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open(%q): %v", root, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestHydrate_NilSession_ReturnsError(t *testing.T) {
	store := openTestStore(t)
	if err := store.Hydrate(nil); err == nil {
		t.Fatal("Hydrate(nil) returned nil error")
	}
}

func TestHydrate_EmptySessionID_ReturnsError(t *testing.T) {
	store := openTestStore(t)
	if err := store.Hydrate(&Session{}); err == nil {
		t.Fatal("Hydrate({ID:\"\"}) returned nil error")
	}
}

func TestHydrate_UnknownSession_ReturnsNotFound(t *testing.T) {
	store := openTestStore(t)
	err := store.Hydrate(&Session{ID: "does-not-exist"})
	if err == nil {
		t.Fatal("Hydrate(unknown) returned nil error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want errors.Is(err, ErrNotFound)", err)
	}
}

// hydrateBasicFixture is the canonical small fixture used by EventCount
// and CompactBoundary tests. Three assistant lines share msg_X (one
// logical response, split), two share msg_Y, plus tool_use events and a
// compact_boundary. UUIDs are linear.
const hydrateBasicFixture = `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:00.000Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:01.000Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","message":{"id":"msg_X","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"thinking"}],"stop_reason":null,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_X"}
{"type":"assistant","uuid":"a-2","parentUuid":"a-1","sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:01.100Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","message":{"id":"msg_X","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"more"}],"stop_reason":null,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_X"}
{"type":"assistant","uuid":"a-3","parentUuid":"a-2","sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:01.200Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","message":{"id":"msg_X","role":"assistant","model":"claude-opus-4-7","content":[{"type":"tool_use","id":"tu_read_1","name":"Read","input":{"path":"a"}}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_X"}
{"type":"user","uuid":"u-2","parentUuid":"a-3","sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:02.000Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","sourceToolUseID":"tu_read_1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_read_1","is_error":false,"content":"file a"}]}}
{"type":"assistant","uuid":"a-4","parentUuid":"u-2","sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:03.000Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","message":{"id":"msg_Y","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"step"}],"stop_reason":null,"usage":{"input_tokens":80,"output_tokens":40,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_Y"}
{"type":"assistant","uuid":"a-5","parentUuid":"a-4","sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:03.100Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","message":{"id":"msg_Y","role":"assistant","model":"claude-opus-4-7","content":[{"type":"tool_use","id":"tu_read_2","name":"Read","input":{"path":"b"}},{"type":"tool_use","id":"tu_bash_1","name":"Bash","input":{"command":"ls"}}],"stop_reason":"tool_use","usage":{"input_tokens":80,"output_tokens":40,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_Y"}
{"type":"system","uuid":"sys-1","parentUuid":"a-5","sessionId":"hydrate-basic","timestamp":"2026-05-10T10:00:04.000Z","cwd":"/work/a","gitBranch":"main","version":"2.1.143","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":9999,"postTokens":4242}}
`

// Expected events from hydrateBasicFixture (matches what events.go emits):
//   1. user u-1
//   2. assistant a-1 (msg_X line 1)
//   3. assistant a-2 (msg_X line 2)
//   4. assistant a-3 (msg_X line 3) + tool_use Read (tu_read_1)  → 2 events
//   5. tool_result u-2
//   6. assistant a-4 (msg_Y line 1)
//   7. assistant a-5 (msg_Y line 2) + tool_use Read + tool_use Bash → 3 events
//   8. compact_boundary sys-1
// Total events: 1 + 1 + 1 + 2 + 1 + 1 + 3 + 1 = 11
const hydrateBasicEventCount = 11

// Token dedup: msg_X (100/50) once, msg_Y (80/40) once → 180 / 90.
var hydrateBasicTokens = TokenStats{Input: 180, Output: 90}

// Tools across the fixture: Read × 2, Bash × 1.
var hydrateBasicTools = map[string]int{"Read": 2, "Bash": 1}

func TestHydrate_EventCountMatches(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-a", "hydrate-basic", hydrateBasicFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session(%q): %v", id, err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if sess.EventCount != hydrateBasicEventCount {
		t.Errorf("EventCount=%d, want %d", sess.EventCount, hydrateBasicEventCount)
	}
}

func TestHydrate_TokenDedupByMessageID(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-a", "hydrate-basic", hydrateBasicFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session(%q): %v", id, err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if sess.Tokens != hydrateBasicTokens {
		t.Errorf("Tokens=%+v, want %+v", sess.Tokens, hydrateBasicTokens)
	}
	// A naive sum would be 100*3 + 80*2 = 460 input, 50*3 + 40*2 = 230 output.
	// Assert we're not in that worst case.
	if sess.Tokens.Input >= 460 || sess.Tokens.Output >= 230 {
		t.Errorf("Tokens look un-deduped: %+v", sess.Tokens)
	}
}

func TestHydrate_ToolsCounted(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-a", "hydrate-basic", hydrateBasicFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session(%q): %v", id, err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if !reflect.DeepEqual(sess.Tools, hydrateBasicTools) {
		t.Errorf("Tools=%v, want %v", sess.Tools, hydrateBasicTools)
	}
}

// hydrateNoToolsFixture has assistant text but no tool_use blocks.
const hydrateNoToolsFixture = `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"hydrate-notools","timestamp":"2026-05-10T10:00:00.000Z","cwd":"/work/x","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","sessionId":"hydrate-notools","timestamp":"2026-05-10T10:00:01.000Z","cwd":"/work/x","gitBranch":"main","version":"2.1.143","message":{"id":"msg_only","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"no tools"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_a"}
`

func TestHydrate_ToolsMapNonNilEvenWhenEmpty(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-x", "hydrate-notools", hydrateNoToolsFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if sess.Tools == nil {
		t.Fatal("Tools is nil; want non-nil empty map")
	}
	if len(sess.Tools) != 0 {
		t.Errorf("len(Tools)=%d, want 0", len(sess.Tools))
	}
}

// hydrateCwdsFixture exposes 3 distinct cwds (/a, /b, /a, /c, /a)
// across user lines. Expected distinct order: [/a /b /c].
const hydrateCwdsFixture = `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"hydrate-cwds","timestamp":"2026-05-10T10:00:00.000Z","cwd":"/a","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"one"}}
{"type":"user","uuid":"u-2","parentUuid":"u-1","sessionId":"hydrate-cwds","timestamp":"2026-05-10T10:00:01.000Z","cwd":"/b","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"two"}}
{"type":"user","uuid":"u-3","parentUuid":"u-2","sessionId":"hydrate-cwds","timestamp":"2026-05-10T10:00:02.000Z","cwd":"/a","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"three"}}
{"type":"user","uuid":"u-4","parentUuid":"u-3","sessionId":"hydrate-cwds","timestamp":"2026-05-10T10:00:03.000Z","cwd":"/c","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"four"}}
{"type":"user","uuid":"u-5","parentUuid":"u-4","sessionId":"hydrate-cwds","timestamp":"2026-05-10T10:00:04.000Z","cwd":"/a","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"five"}}
`

func TestHydrate_CwdsFullSet(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-cwds", "hydrate-cwds", hydrateCwdsFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	want := []string{"/a", "/b", "/c"}
	if !reflect.DeepEqual(sess.Cwds, want) {
		t.Errorf("Cwds=%v, want %v", sess.Cwds, want)
	}
}

// hydrateCompactFixture has one assistant line plus a compact_boundary
// line carrying preTokens/postTokens that MUST NOT contribute to Tokens.
const hydrateCompactFixture = `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"hydrate-compact","timestamp":"2026-05-10T10:00:00.000Z","cwd":"/c","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","sessionId":"hydrate-compact","timestamp":"2026-05-10T10:00:01.000Z","cwd":"/c","gitBranch":"main","version":"2.1.143","message":{"id":"msg_only","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"reply"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_a"}
{"type":"system","uuid":"sys-1","parentUuid":"a-1","sessionId":"hydrate-compact","timestamp":"2026-05-10T10:00:02.000Z","cwd":"/c","gitBranch":"main","version":"2.1.143","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":99999,"postTokens":11111}}
`

func TestHydrate_CompactBoundaryNotInTokens(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-compact", "hydrate-compact", hydrateCompactFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	want := TokenStats{Input: 7, Output: 3}
	if sess.Tokens != want {
		t.Errorf("Tokens=%+v, want %+v (compact_boundary leaked in)", sess.Tokens, want)
	}
}

// hydrateMalformedFixture has a structurally bad JSON line.
const hydrateMalformedFixture = `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"hydrate-bad","timestamp":"2026-05-10T10:00:00.000Z","cwd":"/c","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hi"}}
{not valid json at all
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","sessionId":"hydrate-bad","timestamp":"2026-05-10T10:00:01.000Z","cwd":"/c","gitBranch":"main","version":"2.1.143","message":{"id":"m","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req"}
`

func TestHydrate_MalformedLineReturnsError(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-bad", "hydrate-bad", hydrateMalformedFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	err = store.Hydrate(sess)
	if err == nil {
		t.Fatal("Hydrate returned nil error for malformed input")
	}
	if !strings.Contains(err.Error(), "hydrate") {
		t.Errorf("err does not mention hydrate context: %v", err)
	}
	if !strings.Contains(err.Error(), id) {
		t.Errorf("err does not mention session id: %v", err)
	}
}

func TestHydrate_Idempotent(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-a", "hydrate-basic", hydrateBasicFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate #1: %v", err)
	}
	first := *sess
	firstTools := copyMap(sess.Tools)
	firstCwds := append([]string(nil), sess.Cwds...)

	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate #2: %v", err)
	}

	if sess.EventCount != first.EventCount {
		t.Errorf("EventCount drifted: %d → %d", first.EventCount, sess.EventCount)
	}
	if sess.Tokens != first.Tokens {
		t.Errorf("Tokens drifted: %+v → %+v", first.Tokens, sess.Tokens)
	}
	if !reflect.DeepEqual(sess.Tools, firstTools) {
		t.Errorf("Tools drifted: %v → %v", firstTools, sess.Tools)
	}
	if !reflect.DeepEqual(sess.Cwds, firstCwds) {
		t.Errorf("Cwds drifted: %v → %v", firstCwds, sess.Cwds)
	}
}

func TestHydrate_ConcurrentSameSession_SingleFlight(t *testing.T) {
	t.Parallel()
	root, id, _ := writeHydrateFixture(t, "-work-a", "hydrate-basic", hydrateBasicFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	// Hold the scanner inside the gate until all N goroutines have
	// attached via LoadOrStore. The gate hook fires once per Hydrate
	// call right after attachment; counting those is the only
	// deterministic way to know the gate is fully populated.
	//
	// Both hooks filter by sess.ID: the package-level hook pointers
	// are global, so parallel tests calling Hydrate on other sessions
	// would otherwise increment our counter and release the scanner
	// before our followers have arrived.
	const N = 10
	var scans atomic.Int32
	var gateArrivals atomic.Int32
	allAttached := make(chan struct{})
	release := make(chan struct{})

	prevGate := hydrateGateHook.Load()
	gateHook := func(id string, _ bool) {
		if id != sess.ID {
			return
		}
		if gateArrivals.Add(1) == N {
			close(allAttached)
		}
	}
	hydrateGateHook.Store(&gateHook)
	t.Cleanup(func() { hydrateGateHook.Store(prevGate) })

	prevScan := hydrateScanHook.Load()
	scanHook := func(id string) {
		if id != sess.ID {
			return
		}
		scans.Add(1)
		<-release
	}
	hydrateScanHook.Store(&scanHook)
	t.Cleanup(func() { hydrateScanHook.Store(prevScan) })

	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.Hydrate(sess)
		}()
	}

	<-allAttached
	close(release)

	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Hydrate concurrent err: %v", err)
		}
	}

	if got := scans.Load(); got != 1 {
		t.Errorf("scans=%d, want 1 (single-flight failed)", got)
	}

	// All goroutines should observe the same final state.
	if sess.EventCount != hydrateBasicEventCount {
		t.Errorf("EventCount=%d, want %d", sess.EventCount, hydrateBasicEventCount)
	}
	if sess.Tokens != hydrateBasicTokens {
		t.Errorf("Tokens=%+v, want %+v", sess.Tokens, hydrateBasicTokens)
	}
	if !reflect.DeepEqual(sess.Tools, hydrateBasicTools) {
		t.Errorf("Tools=%v, want %v", sess.Tools, hydrateBasicTools)
	}
}

func TestHydrate_ConcurrentDifferentSessions_AllProgress(t *testing.T) {
	t.Parallel()

	// Create 5 distinct sessions under one root.
	root := t.TempDir()
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		projDir := filepath.Join(root, "-work-"+string(rune('a'+i)))
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			t.Fatal(err)
		}
		ids[i] = "concurrent-0000-0000-0000-00000000000" + string(rune('0'+i))
		// Use a minimal per-session payload — one user, one assistant.
		content := `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"` + ids[i] + `","timestamp":"2026-05-10T10:00:00.000Z","cwd":"/p/` + string(rune('a'+i)) + `","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","sessionId":"` + ids[i] + `","timestamp":"2026-05-10T10:00:01.000Z","cwd":"/p/` + string(rune('a'+i)) + `","gitBranch":"main","version":"2.1.143","message":{"id":"m","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"r"}
`
		if err := os.WriteFile(filepath.Join(projDir, ids[i]+".jsonl"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := openWith(t, root)

	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := store.Session(id)
			if err != nil {
				errs <- err
				return
			}
			errs <- store.Hydrate(sess)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Hydrate err: %v", err)
		}
	}

	for _, id := range ids {
		sess, err := store.Session(id)
		if err != nil {
			t.Fatalf("Session(%q): %v", id, err)
		}
		if sess.EventCount != 2 {
			t.Errorf("session %q EventCount=%d, want 2", id, sess.EventCount)
		}
		if sess.Tokens != (TokenStats{Input: 1, Output: 1}) {
			t.Errorf("session %q Tokens=%+v, want {1 1 0 0}", id, sess.Tokens)
		}
	}
}

// copyMap returns a shallow copy of m so tests can hold a snapshot
// across mutations to the original.
func copyMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
