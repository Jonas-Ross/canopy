package sessions

import (
	"context"
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
