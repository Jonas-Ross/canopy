package sessions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// tailBufferSize is the bounded buffer capacity of the channel returned
// by Tail. Slow consumers cause sends to fail rather than block; see
// docs/sessions-interface.md "Backpressure".
const tailBufferSize = 256

// tail wires an fsnotify watcher to a goroutine that scans appended
// bytes off the .jsonl files under the Store's root and forwards
// parsed Events through a bounded buffered channel.
type tailer struct {
	store   *Store
	watcher *fsnotify.Watcher
	out     chan TailItem

	mu      sync.Mutex
	offsets map[string]int64 // last-read offset per JSONL path

	// Cached projection of the root to identify subagent files
	// without re-deriving from path components on every event.
	root string
}

// Tail returns a channel of live events across all sessions in the
// projects root. See docs/sessions-interface.md for the full contract:
// bounded buffer (256), drop-on-overflow, fsnotify-level errors close
// the channel after a final TailItem{Err: ...}, parse errors keep
// streaming, ctx cancellation drains and closes cleanly.
//
// For v1, only "user" and "assistant" lines are surfaced. Side-band
// lines (attachment, system, file-history-snapshot) and meta lines
// (last-prompt, ai-title, permission-mode, …) are filtered.
func (s *Store) Tail(ctx context.Context) (<-chan TailItem, error) {
	s.mu.RLock()
	root := s.root
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return nil, errors.New("sessions: store is closed")
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("sessions: new watcher: %w", err)
	}

	t := &tailer{
		store:   s,
		watcher: w,
		out:     make(chan TailItem, tailBufferSize),
		offsets: make(map[string]int64),
		root:    root,
	}

	// Walk the tree, add watches for every directory, and record the
	// current size of every .jsonl file so subsequent writes append
	// from a known offset.
	if err := t.seedFromRoot(); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("sessions: seed watcher: %w", err)
	}

	go t.run(ctx)
	return t.out, nil
}

// TailStats returns the live-tail observability counters. Currently
// only tracks the drop counter; thread-safe; the counter accumulates
// over the lifetime of the Store across any number of Tail invocations.
func (s *Store) TailStats() TailStats {
	return TailStats{Dropped: s.tailDropped.Load()}
}

// seedFromRoot is called once before run() starts. It (a) adds an
// fsnotify watch on the root and every existing subdirectory and
// (b) records the current size of every .jsonl file so the first
// Write event after Tail starts treats only the appended bytes as new.
//
// fsnotify is non-recursive on macOS/Linux; explicit walking is
// required. New directories that appear later get watches added in
// run() on the corresponding Create events.
func (t *tailer) seedFromRoot() error {
	if t.root == "" {
		return errors.New("empty root")
	}

	info, err := os.Stat(t.root)
	if err != nil {
		// Root doesn't exist yet — that's not fatal. Watch the
		// parent if we can so a future create of the root is
		// observable. For v1 simplicity, error out instead; the
		// aggregator can decide when to retry.
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("root %q is not a directory", t.root)
	}

	return filepath.WalkDir(t.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Best-effort: a single missing dir shouldn't kill the seed.
			return nil
		}
		if d.IsDir() {
			if err := t.watcher.Add(path); err != nil {
				// Some directories may be inaccessible; that's OK.
				return nil
			}
			return nil
		}
		if isJSONLPath(path) {
			fi, err := os.Stat(path)
			if err != nil {
				return nil
			}
			t.mu.Lock()
			t.offsets[path] = fi.Size()
			t.mu.Unlock()
		}
		return nil
	})
}

// run is the long-lived goroutine fed by fsnotify. It exits on ctx
// cancellation or on a fatal watcher error; in either case the output
// channel is closed and the watcher released.
func (t *tailer) run(ctx context.Context) {
	defer close(t.out)
	defer func() { _ = t.watcher.Close() }()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-t.watcher.Events:
			if !ok {
				return
			}
			t.handleEvent(ctx, ev)
		case err, ok := <-t.watcher.Errors:
			if !ok {
				return
			}
			if err == nil {
				continue
			}
			// fsnotify-level failure: surface as a final
			// TailItem{Err: ...} and tear down. The doc
			// comment promises the channel closes shortly
			// after.
			t.emit(ctx, TailItem{Err: fmt.Errorf("sessions: watcher: %w", err)})
			return
		}
	}
}

// handleEvent dispatches one fsnotify event to the appropriate handler.
func (t *tailer) handleEvent(ctx context.Context, ev fsnotify.Event) {
	switch {
	case ev.Op&fsnotify.Create != 0:
		t.handleCreate(ctx, ev.Name)
	case ev.Op&fsnotify.Write != 0:
		if isJSONLPath(ev.Name) {
			t.scanAppend(ctx, ev.Name)
		}
	case ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		// Drop our offset for this path; we'll re-seed on a future
		// Create. fsnotify auto-removes its watch on rename/remove
		// for files, but explicit removal is cheap and idempotent.
		t.mu.Lock()
		delete(t.offsets, ev.Name)
		t.mu.Unlock()
	}
	// Chmod and others: no-op.
}

// handleCreate processes a Create event. It distinguishes new
// directories (which need a fresh watch and a recursive walk to
// capture children that may appear before we get our own Create event
// for them) from new .jsonl files (which start at offset 0).
func (t *tailer) handleCreate(ctx context.Context, path string) {
	info, err := os.Stat(path)
	if err != nil {
		// Created and immediately gone, or a race we lost. Ignore.
		return
	}
	if info.IsDir() {
		// Walk the new subtree. A fast-created file inside the new
		// dir may have appeared before our handler runs.
		_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if d.IsDir() {
				_ = t.watcher.Add(p)
				return nil
			}
			if isJSONLPath(p) {
				t.scanAppend(ctx, p)
			}
			return nil
		})
		return
	}
	if isJSONLPath(path) {
		// New JSONL file: start at offset 0 and read whatever's
		// already there. Subsequent Write events progress from the
		// updated offset.
		t.mu.Lock()
		if _, ok := t.offsets[path]; !ok {
			t.offsets[path] = 0
		}
		t.mu.Unlock()
		t.scanAppend(ctx, path)
	}
}

// scanAppend reads bytes appended past the recorded offset for path,
// parses each new line as an Event (filtering side-band/meta), and
// emits TailItems for every match. It tolerates partial trailing
// lines: the offset advances only across complete newline-terminated
// records.
func (t *tailer) scanAppend(ctx context.Context, path string) {
	t.mu.Lock()
	off := t.offsets[path]
	t.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		// File may have been removed between event and open.
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return
	}
	size := fi.Size()
	if size < off {
		// Truncated. Restart from zero — this is a rare case
		// (manual edits, log rotation) but mustn't crash.
		off = 0
	}
	if size == off {
		return
	}

	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return
	}

	// Read exactly the appended span so we can compute newline-
	// terminated boundaries precisely. The cap is large enough for
	// any realistic single append; over 10MiB we fall back to
	// scanner-with-buffer.
	span := size - off
	const inMemoryCap = 1 << 20 // 1 MiB
	var (
		consumed int64
		sessionID = sessionIDFromPath(path)
	)

	if span <= inMemoryCap {
		buf := make([]byte, span)
		n, err := io.ReadFull(f, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return
		}
		buf = buf[:n]
		// Walk newline-terminated records; preserve any trailing
		// partial line by leaving its bytes unconsumed.
		for {
			idx := bytes.IndexByte(buf[consumed:], '\n')
			if idx < 0 {
				break
			}
			line := buf[consumed : int(consumed)+idx]
			consumed += int64(idx) + 1
			t.processLine(ctx, path, sessionID, line)
		}
	} else {
		// Large append. Use a bufio.Scanner sized for very long
		// JSONL lines; advance offset by the bytes consumed.
		// Trailing partial line is left for the next Write event.
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), maxScanLineSize)
		for sc.Scan() {
			line := sc.Bytes()
			t.processLine(ctx, path, sessionID, line)
			consumed += int64(len(line)) + 1
		}
		if err := sc.Err(); err != nil {
			// Don't surface as a fatal watcher error; bad
			// line/oversize line gets reported per-line above.
			// Just stop here and try again on the next Write.
		}
	}

	t.mu.Lock()
	t.offsets[path] = off + consumed
	t.mu.Unlock()
}

// processLine parses one JSONL record and emits either an Event or a
// parse error on the output channel. Side-band / meta / unhandled
// types are silently skipped; only "user" and "assistant" conversation
// events are surfaced per the v1 Tail scope.
func (t *tailer) processLine(ctx context.Context, path, sessionID string, line []byte) {
	if len(line) == 0 {
		return
	}
	// Drop trailing CR if any (defensive against rare CRLF-written lines).
	if line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		return
	}
	ev, ok, err := decodeTailLine(line, sessionID)
	if err != nil {
		t.emit(ctx, TailItem{Err: fmt.Errorf("sessions: parse %s: %w", path, err)})
		return
	}
	if !ok {
		return
	}
	t.emit(ctx, TailItem{Event: ev})
}

// emit sends one TailItem on the output channel without blocking. If
// the buffer is full, the item is dropped and the Store-wide drop
// counter is incremented. emit is also a fast path-out on ctx cancel
// so the run goroutine can exit promptly.
func (t *tailer) emit(ctx context.Context, item TailItem) {
	select {
	case <-ctx.Done():
		return
	case t.out <- item:
		return
	default:
	}
	// Channel is full. Either the consumer is slow (drop and keep
	// going) or we're racing ctx cancel (which we want to honor).
	select {
	case <-ctx.Done():
		return
	case t.out <- item:
		// Drained just in time.
	default:
		t.store.tailDropped.Add(1)
	}
}

// --- parsing helpers (self-contained; parse.go is owned by another agent) ---

// tailRawLine is the minimal projection of a JSONL line needed for the
// Tail event scope. Only user/assistant fields are decoded; meta lines
// are filtered earlier by the Type check.
type tailRawLine struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Timestamp  string          `json:"timestamp"`
	IsMeta     bool            `json:"isMeta"`
	Message    json.RawMessage `json:"message"`
}

// tailRawUserMessage decodes the .message envelope of a user line. The
// content field is intentionally a RawMessage so we can detect "bare
// string" vs "array of blocks" without unmarshalling the blocks (which
// Tail doesn't need for v1).
type tailRawUserMessage struct {
	Content json.RawMessage `json:"content"`
}

// tailRawAssistantMessage decodes just the bits of the assistant
// envelope that Tail's v1 surface needs: model + usage. Text content
// is deliberately skipped — the consumer that needs Text uses Events().
type tailRawAssistantMessage struct {
	ID    string         `json:"id"`
	Model string         `json:"model"`
	Usage *tailRawUsage  `json:"usage"`
}

type tailRawUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// decodeTailLine parses a single JSONL line into an Event for Tail's
// v1 scope. Returns (ev, true, nil) for a usable conversation event,
// (zero, false, nil) for a line that should be silently skipped (meta,
// side-band, unknown type, isMeta), or (zero, false, err) for a JSON
// parse failure.
//
// The fallbackSessionID is used when the line itself does not carry a
// sessionId field (defensive — subagent lines reliably do; top-level
// lines reliably do; this is here in case of malformed inputs that
// happen to be otherwise valid JSON).
func decodeTailLine(line []byte, fallbackSessionID string) (Event, bool, error) {
	var raw tailRawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, false, err
	}
	// Filter early: only user/assistant; never meta lines.
	if raw.Type != "user" && raw.Type != "assistant" {
		return Event{}, false, nil
	}
	if raw.IsMeta {
		return Event{}, false, nil
	}
	if raw.UUID == "" {
		// Conversation lines always have a uuid. A user/assistant
		// line without one is malformed; skip silently — emitting
		// an error here would be noisy and the schema doc treats
		// this as a meta indicator.
		return Event{}, false, nil
	}

	ts, _ := parseTimestamp(raw.Timestamp)

	sid := fallbackSessionID
	if raw.SessionID != "" && fallbackSessionID == "" {
		sid = raw.SessionID
	}
	if sid == "" {
		sid = raw.SessionID
	}

	ev := Event{
		SessionID:  sid,
		UUID:       raw.UUID,
		ParentUUID: raw.ParentUUID,
		Timestamp:  ts,
	}

	switch raw.Type {
	case "user":
		um := &UserMessage{}
		if len(raw.Message) > 0 {
			var rm tailRawUserMessage
			if err := json.Unmarshal(raw.Message, &rm); err == nil && len(rm.Content) > 0 {
				// Only fill Text when content is a bare JSON
				// string. Array forms (tool results, image
				// pastes) are not surfaced by Tail in v1.
				if isJSONString(rm.Content) {
					var s string
					if err := json.Unmarshal(rm.Content, &s); err == nil {
						um.Text = s
					}
				}
			}
		}
		ev.Kind = EventUser
		ev.User = um
	case "assistant":
		am := &AssistantMessage{}
		if len(raw.Message) > 0 {
			var rm tailRawAssistantMessage
			if err := json.Unmarshal(raw.Message, &rm); err == nil {
				am.Model = rm.Model
				am.MessageID = rm.ID
				if rm.Usage != nil {
					am.Tokens = TokenStats{
						Input:         rm.Usage.InputTokens,
						Output:        rm.Usage.OutputTokens,
						CacheRead:     rm.Usage.CacheReadInputTokens,
						CacheCreation: rm.Usage.CacheCreationInputTokens,
					}
				}
			}
		}
		ev.Kind = EventAssistant
		ev.Assistant = am
	}
	return ev, true, nil
}

// isJSONString reports whether raw is a JSON-encoded string literal
// (i.e. starts with a double-quote after any leading whitespace).
func isJSONString(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			return true
		default:
			return false
		}
	}
	return false
}

// --- path helpers ---

// isJSONLPath reports whether path looks like a Claude Code JSONL file.
// Hidden temp files like ".foo.jsonl.swp" or rename-target artifacts
// like "foo.jsonl~" are excluded.
func isJSONLPath(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return false
	}
	return strings.HasSuffix(base, ".jsonl")
}

// sessionIDFromPath derives the canonical Session.ID for the file at
// path using the same rules as buildSession's composeID. Subagent
// files yield "<parentSessionId>#<agentId>"; top-level files yield the
// filename stem.
//
// This is duplicated logic with composeID in store.go, but kept here
// to avoid coupling tail.go to the precise internal helper name; the
// rule is documented in docs/jsonl-schema.md §1.
func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	dir := filepath.Dir(path)
	// Subagent layout: <root>/<project>/<parentId>/subagents/agent-<agentId>.jsonl
	if filepath.Base(dir) == "subagents" {
		parent := filepath.Base(filepath.Dir(dir))
		agent := strings.TrimPrefix(stem, "agent-")
		return parent + "#" + agent
	}
	return stem
}
