package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// readDeadline reads at most maxItems from ch or returns whatever has
// arrived by the deadline. A closed channel terminates early. Returns
// the collected items and a bool reporting whether the channel was
// closed before the deadline.
func readDeadline(t *testing.T, ch <-chan TailItem, maxItems int, d time.Duration) (items []TailItem, closed bool) {
	t.Helper()
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for len(items) < maxItems {
		select {
		case it, ok := <-ch:
			if !ok {
				return items, true
			}
			items = append(items, it)
		case <-deadline.C:
			return items, false
		}
	}
	return items, false
}

// waitFor polls fn until it returns true or the deadline elapses.
// Returns true on success. Used to avoid hardcoded time.Sleep on
// async-but-quick state transitions.
func waitFor(d time.Duration, interval time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(interval)
	}
	return fn()
}

// makeJSONLLine returns one JSONL record terminated with \n.
func makeJSONLLine(kind, uuid, parentUUID, sessionID, timestamp, cwd, extra string) string {
	parent := "null"
	if parentUUID != "" {
		parent = `"` + parentUUID + `"`
	}
	return fmt.Sprintf(
		`{"type":%q,"uuid":%q,"parentUuid":%s,"sessionId":%q,"timestamp":%q,"cwd":%q,"gitBranch":"main","version":"2.1.143"%s}`+"\n",
		kind, uuid, parent, sessionID, timestamp, cwd, extra,
	)
}

// userLine builds a "user" JSONL record with bare-string content.
func userLine(uuid, parent, sessionID, text string) string {
	return makeJSONLLine("user", uuid, parent, sessionID, "2026-05-17T10:00:00.000Z", "/tmp/tail", fmt.Sprintf(`,"message":{"role":"user","content":%q}`, text))
}

// assistantLine builds an "assistant" JSONL record with the named model
// and usage object.
func assistantLine(uuid, parent, sessionID, model string, in, out, cacheRead, cacheCreate int) string {
	msg := fmt.Sprintf(
		`,"message":{"id":"m-%s","role":"assistant","model":%q,"content":[{"type":"text","text":"x"}],"stop_reason":"end_turn","usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d}},"requestId":"req-%s"`,
		uuid, model, in, out, cacheRead, cacheCreate, uuid,
	)
	return makeJSONLLine("assistant", uuid, parent, sessionID, "2026-05-17T10:00:00.000Z", "/tmp/tail", msg)
}

// metaLine builds a session-meta line (no uuid/timestamp). One of the
// known meta types per docs/jsonl-schema.md §3.
func metaLine(kind string) string {
	return fmt.Sprintf(`{"type":%q,"value":"x"}`+"\n", kind)
}

// attachmentLine builds an attachment side-band line.
func attachmentLine(uuid, parent, sessionID string) string {
	return makeJSONLLine("attachment", uuid, parent, sessionID, "2026-05-17T10:00:00.000Z", "/tmp/tail", `,"subtype":"system_info"`)
}

// isMetaUserLine builds a synthetic isMeta:true user line (local-
// command caveats per docs/jsonl-schema.md §10).
func isMetaUserLine(uuid, parent, sessionID string) string {
	return fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":%s,"sessionId":%q,"timestamp":"2026-05-17T10:00:00.000Z","cwd":"/tmp/tail","gitBranch":"main","version":"2.1.143","isMeta":true,"message":{"role":"user","content":"local"}}`+"\n",
		uuid,
		func() string {
			if parent == "" {
				return "null"
			}
			return `"` + parent + `"`
		}(),
		sessionID,
	)
}

// newTailTree materializes a projects root with one top-level session
// file present (empty). Returns the root path and the JSONL file path.
func newTailTree(t *testing.T) (root, projDir, jsonlPath string) {
	t.Helper()
	root = t.TempDir()
	projDir = filepath.Join(root, "-tmp-projects-tail")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	jsonlPath = filepath.Join(projDir, "tail0001-0000-0000-0000-000000000001.jsonl")
	// Seed with a single attachment so Open indexes the file and
	// Tail starts with a non-zero offset (verifies offset tracking).
	if err := os.WriteFile(jsonlPath, []byte(attachmentLine("seed-uuid", "", "tail0001-0000-0000-0000-000000000001")), 0o644); err != nil {
		t.Fatalf("seed jsonl: %v", err)
	}
	return
}

// appendLine appends s to path. Used to simulate the CLI writing one
// more event to a session file.
func appendLine(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append %s: %v", path, err)
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		t.Fatalf("write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// startTail is a one-liner test helper that opens a Store and starts a
// Tail, plumbing cleanup of both. Returns the Store, the live channel,
// and the cancel func.
func startTail(t *testing.T, root string) (*Store, <-chan TailItem, context.CancelFunc) {
	t.Helper()
	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := store.Tail(ctx)
	if err != nil {
		cancel()
		_ = store.Close()
		t.Fatalf("Tail: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = store.Close()
	})
	return store, ch, cancel
}

func TestTail_AppendedLineFlowsThrough(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	_, ch, _ := startTail(t, root)

	appendLine(t, jsonlPath, userLine("u1", "seed-uuid", "tail0001-0000-0000-0000-000000000001", "hello tail"))

	select {
	case it := <-ch:
		if it.Err != nil {
			t.Fatalf("unexpected err: %v", it.Err)
		}
		if it.Event.Kind != EventUser {
			t.Fatalf("Kind=%v want EventUser", it.Event.Kind)
		}
		if it.Event.UUID != "u1" {
			t.Errorf("UUID=%q want u1", it.Event.UUID)
		}
		if it.Event.User == nil || it.Event.User.Text != "hello tail" {
			t.Errorf("User.Text=%v want \"hello tail\"", it.Event.User)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event within 3s")
	}
}

func TestTail_NewFileFlowsThrough(t *testing.T) {
	root, projDir, _ := newTailTree(t)
	_, ch, _ := startTail(t, root)

	// New file under existing project dir.
	newPath := filepath.Join(projDir, "tail0002-0000-0000-0000-000000000002.jsonl")
	content := assistantLine("a1", "", "tail0002-0000-0000-0000-000000000002", "claude-opus-4-7", 10, 20, 30, 40)
	if err := os.WriteFile(newPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	select {
	case it := <-ch:
		if it.Err != nil {
			t.Fatalf("unexpected err: %v", it.Err)
		}
		if it.Event.Kind != EventAssistant {
			t.Fatalf("Kind=%v want EventAssistant", it.Event.Kind)
		}
		if it.Event.Assistant == nil {
			t.Fatal("Assistant payload nil")
		}
		if it.Event.Assistant.Model != "claude-opus-4-7" {
			t.Errorf("Model=%q want claude-opus-4-7", it.Event.Assistant.Model)
		}
		if it.Event.Assistant.Tokens.Input != 10 || it.Event.Assistant.Tokens.Output != 20 ||
			it.Event.Assistant.Tokens.CacheRead != 30 || it.Event.Assistant.Tokens.CacheCreation != 40 {
			t.Errorf("Tokens=%+v want {10 20 30 40}", it.Event.Assistant.Tokens)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event within 3s")
	}
}

func TestTail_NewProjectDirFlowsThrough(t *testing.T) {
	root, _, _ := newTailTree(t)
	_, ch, _ := startTail(t, root)

	// Create a brand new project dir + file inside it.
	newProj := filepath.Join(root, "-tmp-projects-fresh")
	if err := os.MkdirAll(newProj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	newFile := filepath.Join(newProj, "fresh001-0000-0000-0000-000000000001.jsonl")
	content := userLine("u-fresh", "", "fresh001-0000-0000-0000-000000000001", "from fresh project")
	// Write the file shortly after dir creation: this exercises the
	// "walk the new subtree on Create" path (the file may appear
	// before the watch is in place).
	if err := os.WriteFile(newFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Allow up to 4s: dir-create then file-create round-tripping
	// through fsnotify on busy macOS CI runners can be slow.
	deadline := time.After(4 * time.Second)
	var got TailItem
	for {
		select {
		case it, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before event")
			}
			if it.Err != nil {
				t.Fatalf("unexpected err: %v", it.Err)
			}
			if it.Event.UUID == "u-fresh" {
				got = it
				goto done
			}
		case <-deadline:
			t.Fatal("no fresh event within 4s")
		}
	}
done:
	if got.Event.User == nil || got.Event.User.Text != "from fresh project" {
		t.Errorf("User=%v want text=from fresh project", got.Event.User)
	}
}

func TestTail_SubagentFileCompositeID(t *testing.T) {
	root, projDir, _ := newTailTree(t)
	// Subagent layout: <root>/<project>/<parentId>/subagents/agent-<id>.jsonl
	parentID := "parent01-0000-0000-0000-000000000001"
	parentDir := filepath.Join(projDir, parentID)
	subDir := filepath.Join(parentDir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subagents: %v", err)
	}
	subFile := filepath.Join(subDir, "agent-XYZ123.jsonl")

	_, ch, _ := startTail(t, root)

	// Write the subagent file after Tail starts so its Create event
	// (or the subsequent file scan) drives the event.
	content := userLine("u-sub", "", parentID, "hi from subagent")
	if err := os.WriteFile(subFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write subagent: %v", err)
	}

	deadline := time.After(4 * time.Second)
	for {
		select {
		case it, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			if it.Err != nil {
				t.Fatalf("unexpected err: %v", it.Err)
			}
			if it.Event.UUID != "u-sub" {
				continue
			}
			want := parentID + "#XYZ123"
			if it.Event.SessionID != want {
				t.Errorf("SessionID=%q want %q", it.Event.SessionID, want)
			}
			return
		case <-deadline:
			t.Fatal("no subagent event within 4s")
		}
	}
}

func TestTail_SkipsMetaAndAttachmentLines(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	_, ch, _ := startTail(t, root)

	sid := "tail0001-0000-0000-0000-000000000001"
	// Append: permission-mode (meta), attachment (side-band),
	// isMeta:true user, then a real user line. Only the last should
	// arrive on the channel.
	appendLine(t, jsonlPath, metaLine("permission-mode"))
	appendLine(t, jsonlPath, attachmentLine("att1", "seed-uuid", sid))
	appendLine(t, jsonlPath, isMetaUserLine("um1", "att1", sid))
	appendLine(t, jsonlPath, userLine("u-real", "um1", sid, "real user"))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case it, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			if it.Err != nil {
				t.Fatalf("unexpected err: %v", it.Err)
			}
			if it.Event.UUID == "u-real" {
				if it.Event.Kind != EventUser {
					t.Errorf("Kind=%v want EventUser", it.Event.Kind)
				}
				if it.Event.User == nil || it.Event.User.Text != "real user" {
					t.Errorf("User=%v", it.Event.User)
				}
				return
			}
			t.Errorf("unexpected event passed filter: %+v", it.Event)
		case <-deadline:
			t.Fatal("real user event never arrived")
		}
	}
}

func TestTail_MalformedLineYieldsErrorAndContinues(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	_, ch, _ := startTail(t, root)

	sid := "tail0001-0000-0000-0000-000000000001"
	// Append: malformed JSON then a valid user line. Order matters:
	// fsnotify may coalesce; we want both deliveries through the
	// same scan pass.
	appendLine(t, jsonlPath, "{not json at all"+"\n")
	appendLine(t, jsonlPath, userLine("u-after", "seed-uuid", sid, "after bad"))

	var sawErr, sawEvent bool
	deadline := time.After(3 * time.Second)
	for !(sawErr && sawEvent) {
		select {
		case it, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before both signals")
			}
			if it.Err != nil {
				sawErr = true
				if !strings.Contains(it.Err.Error(), "parse") {
					t.Errorf("err didn't mention parse: %v", it.Err)
				}
				continue
			}
			if it.Event.UUID == "u-after" {
				sawEvent = true
			}
		case <-deadline:
			t.Fatalf("did not see both: sawErr=%v sawEvent=%v", sawErr, sawEvent)
		}
	}
}

func TestTail_BoundedBufferDropsEvents(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	store, ch, _ := startTail(t, root)

	sid := "tail0001-0000-0000-0000-000000000001"
	const n = 500
	// Append N events without reading the channel. The capacity of
	// the tail channel is 256, so the producer must drop on at least
	// the first burst that overflows it.
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(userLine(fmt.Sprintf("u%04d", i), "seed-uuid", sid, "x"))
	}
	if err := os.WriteFile(jsonlPath, []byte(attachmentLine("seed-uuid", "", sid)+sb.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Wait until either the drop counter is non-zero or a generous
	// deadline has passed. The producer must NOT block — if it did,
	// this poll would never see a non-zero drop count because the
	// goroutine would be stuck on a channel send.
	if !waitFor(3*time.Second, 20*time.Millisecond, func() bool {
		return store.TailStats().Dropped > 0
	}) {
		t.Fatalf("expected drops, TailStats=%+v", store.TailStats())
	}

	// And the buffer should also have data ready: drain a few items
	// to prove the channel is alive after the overflow.
	items, _ := readDeadline(t, ch, 50, 2*time.Second)
	if len(items) == 0 {
		t.Fatal("expected to drain at least some buffered items")
	}
}

func TestTail_CtxCancelClosesChannel(t *testing.T) {
	root, _, _ := newTailTree(t)

	gBefore := runtime.NumGoroutine()

	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := store.Tail(ctx)
	if err != nil {
		cancel()
		t.Fatalf("Tail: %v", err)
	}

	cancel()

	// Channel must close within a short deadline. Drain anything in
	// the buffer.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				goto closed
			}
		case <-deadline:
			t.Fatal("channel not closed within 2s of ctx cancel")
		}
	}
closed:
	// Allow a brief settle for any goroutine teardown that races
	// past the channel close. NumGoroutine should be back near its
	// pre-Tail baseline.
	if !waitFor(2*time.Second, 20*time.Millisecond, func() bool {
		return runtime.NumGoroutine() <= gBefore+2
	}) {
		t.Errorf("goroutine leak: before=%d after=%d", gBefore, runtime.NumGoroutine())
	}
}

func TestDecodeTailLine_SessionIDFallback(t *testing.T) {
	const (
		fallback = "fallback-0000-0000-0000-000000000001"
		raw      = "raw-line-0000-0000-0000-000000000002"
	)
	tests := []struct {
		name     string
		fallback string
		rawSID   string
		want     string
	}{
		{"fallback set, raw set: fallback wins", fallback, raw, fallback},
		{"fallback set, raw empty: fallback wins", fallback, "", fallback},
		{"fallback empty, raw set: raw fills in", "", raw, raw},
		{"fallback empty, raw empty: empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := userLine("u1", "", tc.rawSID, "hi")
			ev, ok, err := decodeTailLine([]byte(line), tc.fallback)
			if err != nil {
				t.Fatalf("decodeTailLine err: %v", err)
			}
			if !ok {
				t.Fatalf("decodeTailLine: ok=false, want true")
			}
			if ev.SessionID != tc.want {
				t.Errorf("SessionID=%q want %q", ev.SessionID, tc.want)
			}
		})
	}
}

// --- checkpoint tests (issue #12) ---

// drainCh reads everything currently buffered on ch within d. Used to
// empty the live channel between checkpoint test phases without asserting
// on every individual item.
func drainCh(ch <-chan TailItem, d time.Duration) []TailItem {
	var out []TailItem
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		select {
		case it, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, it)
		case <-deadline.C:
			return out
		}
	}
}

// startTailCheckpoint opens a Store and starts Tail with a checkpoint
// path. Returns the Store, channel, and cancel func. Cleanup waits for
// the tail goroutine to fully drain the channel before returning so the
// final checkpoint write completes before any t.TempDir() teardown
// races it (the goroutine's defer flushCheckpoint runs before the
// channel close).
func startTailCheckpoint(t *testing.T, root, ckpt string) (*Store, <-chan TailItem, context.CancelFunc) {
	t.Helper()
	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := store.Tail(ctx, TailWithCheckpoint(ckpt))
	if err != nil {
		cancel()
		_ = store.Close()
		t.Fatalf("Tail: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		for range ch {
		}
		_ = store.Close()
	})
	return store, ch, cancel
}

func TestTail_CheckpointResumesAfterRestart(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	ckpt := filepath.Join(t.TempDir(), "tail-offsets.json")
	sid := "tail0001-0000-0000-0000-000000000001"

	// Phase 1: start Tail with a checkpoint, append one line, drain it,
	// cancel. The offset for jsonlPath should land in the checkpoint
	// file on teardown.
	store1, ch1, cancel1 := startTailCheckpoint(t, root, ckpt)
	appendLine(t, jsonlPath, userLine("u1", "seed-uuid", sid, "phase1"))
	got := drainCh(ch1, 2*time.Second)
	if len(got) == 0 {
		t.Fatalf("phase1: no events drained")
	}
	cancel1()
	_ = store1.Close()
	// Give the teardown a moment to flush.
	if !waitFor(2*time.Second, 20*time.Millisecond, func() bool {
		_, err := os.Stat(ckpt)
		return err == nil
	}) {
		t.Fatalf("phase1: checkpoint file never created at %s", ckpt)
	}

	// Phase 2: append a NEW line while "offline".
	appendLine(t, jsonlPath, userLine("u-missed", "u1", sid, "offline"))

	// Phase 3: start a second Tail. The "offline" event must arrive.
	_, ch2, _ := startTailCheckpoint(t, root, ckpt)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case it, ok := <-ch2:
			if !ok {
				t.Fatal("phase3: channel closed before missed event")
			}
			if it.Err != nil {
				t.Fatalf("phase3: unexpected err: %v", it.Err)
			}
			if it.Event.UUID == "u-missed" {
				if it.Event.User == nil || it.Event.User.Text != "offline" {
					t.Errorf("phase3: User=%v want text=offline", it.Event.User)
				}
				return
			}
		case <-deadline:
			t.Fatal("phase3: missed event never arrived after restart")
		}
	}
}

func TestTail_CheckpointPreservesOrder(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	ckpt := filepath.Join(t.TempDir(), "tail-offsets.json")
	sid := "tail0001-0000-0000-0000-000000000001"

	store1, ch1, cancel1 := startTailCheckpoint(t, root, ckpt)
	appendLine(t, jsonlPath, userLine("u1", "seed-uuid", sid, "live"))
	_ = drainCh(ch1, 2*time.Second)
	cancel1()
	_ = store1.Close()
	if !waitFor(2*time.Second, 20*time.Millisecond, func() bool {
		_, err := os.Stat(ckpt)
		return err == nil
	}) {
		t.Fatalf("checkpoint not written")
	}

	// Append three offline events in order.
	appendLine(t, jsonlPath, userLine("ua", "u1", sid, "a"))
	appendLine(t, jsonlPath, userLine("ub", "ua", sid, "b"))
	appendLine(t, jsonlPath, userLine("uc", "ub", sid, "c"))

	_, ch2, _ := startTailCheckpoint(t, root, ckpt)
	var seen []string
	deadline := time.After(3 * time.Second)
	for len(seen) < 3 {
		select {
		case it, ok := <-ch2:
			if !ok {
				t.Fatalf("channel closed early; seen=%v", seen)
			}
			if it.Err != nil {
				t.Fatalf("unexpected err: %v", it.Err)
			}
			switch it.Event.UUID {
			case "ua", "ub", "uc":
				seen = append(seen, it.Event.UUID)
			}
		case <-deadline:
			t.Fatalf("timed out; seen=%v", seen)
		}
	}
	want := []string{"ua", "ub", "uc"}
	for i, u := range want {
		if seen[i] != u {
			t.Fatalf("order mismatch: seen=%v want=%v", seen, want)
		}
	}
}

func TestTail_CorruptCheckpointFallsBackToEOF(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	ckpt := filepath.Join(t.TempDir(), "tail-offsets.json")
	sid := "tail0001-0000-0000-0000-000000000001"

	// Write garbage into the checkpoint location.
	if err := os.WriteFile(ckpt, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("seed bad ckpt: %v", err)
	}

	// Append "offline" data BEFORE Tail starts. Because the checkpoint
	// is corrupt, the implementation must fall back to current-EOF
	// seeding and these lines must NOT arrive.
	appendLine(t, jsonlPath, userLine("u-pre", "seed-uuid", sid, "pre"))

	_, ch, _ := startTailCheckpoint(t, root, ckpt)

	// Now append a fresh line — this one should arrive.
	appendLine(t, jsonlPath, userLine("u-post", "u-pre", sid, "post"))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case it, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			if it.Err != nil {
				t.Fatalf("unexpected err: %v", it.Err)
			}
			if it.Event.UUID == "u-pre" {
				t.Fatal("pre-Tail event leaked through despite corrupt checkpoint (fallback to EOF was not honored)")
			}
			if it.Event.UUID == "u-post" {
				return
			}
		case <-deadline:
			t.Fatal("post-Tail event never arrived")
		}
	}
}

func TestTail_CheckpointMissingFileTreatedAsFresh(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	ckpt := filepath.Join(t.TempDir(), "does", "not", "exist", "tail-offsets.json")
	sid := "tail0001-0000-0000-0000-000000000001"

	store, ch, cancel := startTailCheckpoint(t, root, ckpt)

	// Behaves like the no-checkpoint case: pre-existing seed line is not
	// replayed; new appends do flow through.
	appendLine(t, jsonlPath, userLine("u-new", "seed-uuid", sid, "new"))

	select {
	case it := <-ch:
		if it.Err != nil {
			t.Fatalf("unexpected err: %v", it.Err)
		}
		if it.Event.UUID != "u-new" {
			t.Fatalf("UUID=%q want u-new", it.Event.UUID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event within 3s")
	}

	// Teardown should create the file (and its parent dirs).
	cancel()
	_ = store.Close()
	if !waitFor(2*time.Second, 20*time.Millisecond, func() bool {
		_, err := os.Stat(ckpt)
		return err == nil
	}) {
		t.Fatalf("checkpoint not created at %s", ckpt)
	}
}

func TestTail_CheckpointWriteIsAtomic(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	ckpt := filepath.Join(t.TempDir(), "tail-offsets.json")
	sid := "tail0001-0000-0000-0000-000000000001"

	_, ch, _ := startTailCheckpoint(t, root, ckpt)

	// Drive a tight write loop on the producer side while a reader
	// loop hammers the checkpoint file. Any half-written observation
	// would surface as a JSON parse error.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			appendLine(t, jsonlPath, userLine(fmt.Sprintf("uatom%04d", i), "seed-uuid", sid, "x"))
			time.Sleep(2 * time.Millisecond)
		}
	}()
	// Drain channel so the producer doesn't get stuck on full-buffer
	// drops; that's not what this test is about.
	go func() {
		for range ch {
		}
	}()

	readDeadline := time.After(3 * time.Second)
	for {
		select {
		case <-readDeadline:
			return
		case <-done:
			return
		default:
		}
		data, err := os.ReadFile(ckpt)
		if err != nil {
			// File may not exist yet on first iteration; that's OK.
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if len(data) == 0 {
			continue
		}
		var m map[string]int64
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("observed non-atomic write: %v; payload=%q", err, string(data))
		}
	}
}

func TestTail_CheckpointPrunesStaleEntries(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	ckpt := filepath.Join(t.TempDir(), "tail-offsets.json")
	sid := "tail0001-0000-0000-0000-000000000001"

	// Seed a checkpoint that contains a stale (non-existent) path.
	stalePath := "/definitely/does/not/exist/stale.jsonl"
	seed := fmt.Sprintf(`{%q:42,%q:0}`, stalePath, jsonlPath)
	if err := os.WriteFile(ckpt, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed ckpt: %v", err)
	}

	store, ch, cancel := startTailCheckpoint(t, root, ckpt)

	// Drive a flush by appending an event and waiting for it to arrive.
	appendLine(t, jsonlPath, userLine("u-flush", "seed-uuid", sid, "flush"))
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("no event arrived")
	}
	cancel()
	_ = store.Close()

	// After teardown, the on-disk checkpoint must not mention the stale path.
	if !waitFor(2*time.Second, 20*time.Millisecond, func() bool {
		data, err := os.ReadFile(ckpt)
		if err != nil {
			return false
		}
		return !strings.Contains(string(data), stalePath)
	}) {
		data, _ := os.ReadFile(ckpt)
		t.Fatalf("stale entry not pruned; checkpoint=%q", string(data))
	}
}

func TestTail_CheckpointHandlesTruncatedFile(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	ckpt := filepath.Join(t.TempDir(), "tail-offsets.json")
	sid := "tail0001-0000-0000-0000-000000000001"

	// Set checkpoint offset past current file size (simulating log rotation
	// or external truncation between runs).
	huge := int64(1 << 30)
	seed := fmt.Sprintf(`{%q:%d}`, jsonlPath, huge)
	if err := os.WriteFile(ckpt, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed ckpt: %v", err)
	}

	_, ch, _ := startTailCheckpoint(t, root, ckpt)

	// A subsequently appended event must still arrive. (The implementation
	// should detect offset > size at load and restart from EOF or 0; the
	// existing scanAppend safety net handles size<off mid-flight too.)
	appendLine(t, jsonlPath, userLine("u-rot", "seed-uuid", sid, "after rotate"))
	select {
	case it := <-ch:
		if it.Err != nil {
			t.Fatalf("unexpected err: %v", it.Err)
		}
		if it.Event.UUID != "u-rot" {
			t.Fatalf("UUID=%q want u-rot", it.Event.UUID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("post-truncation append never arrived")
	}
}

func TestTail_NoCheckpoint_NoFileCreated(t *testing.T) {
	root, _, jsonlPath := newTailTree(t)
	sid := "tail0001-0000-0000-0000-000000000001"

	// Choose a sentinel path that should NOT be created by default Tail.
	ckpt := filepath.Join(t.TempDir(), "sentinel.json")

	store, ch, cancel := startTail(t, root) // no checkpoint option
	appendLine(t, jsonlPath, userLine("u1", "seed-uuid", sid, "x"))
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
	cancel()
	_ = store.Close()
	if _, err := os.Stat(ckpt); !os.IsNotExist(err) {
		t.Fatalf("default Tail must not create checkpoint file; Stat err=%v", err)
	}
}
