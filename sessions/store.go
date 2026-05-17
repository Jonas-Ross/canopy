package sessions

import (
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound is returned by Session when the given ID is not present
// in the index. Callers should match it with errors.Is.
var ErrNotFound = errors.New("sessions: session not found")

// cwdIndexEntry is a (Cwd, SessionID) row in the in-memory sorted
// slice that backs SessionsByCwdPrefix. The slice is sorted by Cwd to
// allow binary-search range lookup.
type cwdIndexEntry struct {
	Cwd       string
	SessionID string
}

// Store is the in-memory index over a Claude Code projects root. Safe
// for concurrent use by multiple goroutines.
//
// All exported methods take an internal RWMutex; readers (Sessions,
// Session, Query, SessionsByCwdPrefix) take the read lock and run
// concurrently. Hydrate takes the write lock only while installing its
// final result.
type Store struct {
	mu sync.RWMutex

	// root is the absolute path of the projects root passed to Open.
	root string

	// byID is the primary index keyed by Session.ID.
	byID map[string]*Session

	// ordered is the materialized slice of sessions, sorted by
	// StartedAt descending with ID as the tiebreaker. Sessions() and
	// Query() iterate over this; updating it is allowed only under
	// the write lock.
	ordered []*Session

	// cwdIndex is sorted ascending by Cwd. SessionsByCwdPrefix uses
	// sort.Search to find the matching prefix range.
	cwdIndex []cwdIndexEntry

	// closed flips to true on first Close(); subsequent calls are
	// no-ops.
	closed bool

	// tailDropped accumulates the count of TailItem values discarded
	// because the consumer of a Tail() channel was too slow. Exposed
	// via TailStats(); thread-safe via atomic ops. See tail.go.
	tailDropped atomic.Uint64
}

// Open builds the session index by walking <root>/*/*.jsonl plus the
// subagent subtree at <root>/<projectDir>/<sessionId>/subagents/. It
// reads first and last conversation lines per file to populate metadata;
// it does not read full event bodies. Synchronous: returns only after
// the index is built. Typical root: filepath.Join(home, ".claude", "projects").
func Open(root string, opts ...OpenOption) (*Store, error) {
	cfg := &openConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("sessions: resolve root: %w", err)
	}

	// Discover candidate files. Top-level: <root>/*/*.jsonl.
	// Subagents: <root>/*/*/subagents/*.jsonl.
	topLevel, err := filepath.Glob(filepath.Join(absRoot, "*", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("sessions: glob top-level: %w", err)
	}
	subagents, err := filepath.Glob(filepath.Join(absRoot, "*", "*", "subagents", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("sessions: glob subagents: %w", err)
	}

	type candidate struct {
		path      string
		sidechain bool
	}
	all := make([]candidate, 0, len(topLevel)+len(subagents))
	for _, p := range topLevel {
		all = append(all, candidate{path: p, sidechain: false})
	}
	for _, p := range subagents {
		all = append(all, candidate{path: p, sidechain: true})
	}

	total := len(all)
	sessions := make([]*Session, 0, total)

	for i, c := range all {
		sess, err := buildSession(c.path, c.sidechain)
		if err != nil {
			// Best-effort: ignore unreadable files. A noisy log
			// belongs in higher layers, not here.
			if cfg.progress != nil {
				cfg.progress(i+1, total)
			}
			continue
		}
		if sess == nil {
			// Skipped (meta-only file).
			if cfg.progress != nil {
				cfg.progress(i+1, total)
			}
			continue
		}
		sessions = append(sessions, sess)
		if cfg.progress != nil {
			cfg.progress(i+1, total)
		}
	}

	s := &Store{
		root: absRoot,
		byID: make(map[string]*Session, len(sessions)),
	}
	for _, sess := range sessions {
		s.byID[sess.ID] = sess
	}
	s.ordered = sortSessions(sessions)
	s.cwdIndex = buildCwdIndex(sessions)

	return s, nil
}

// buildSession scans one JSONL file and returns the index entry for it.
// Returns (nil, nil) when the file has no conversation events (meta-
// only files are skipped per the doc).
func buildSession(path string, isSubagent bool) (*Session, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("abs %s: %w", path, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", absPath, err)
	}

	meta, err := readFileMeta(absPath)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", absPath, err)
	}
	if !meta.hasAnyConvLine {
		return nil, nil
	}

	id, parentID := composeID(absPath, isSubagent)

	return &Session{
		ID:              id,
		Path:            absPath,
		Cwds:            dedupeCwds(meta.firstCwd, meta.lastCwd),
		Model:           meta.model,
		StartedAt:       meta.startedAt,
		UpdatedAt:       info.ModTime().UTC(),
		IsSidechain:     isSubagent,
		ParentSessionID: parentID,
	}, nil
}

// composeID derives Session.ID and ParentSessionID from a file path.
//
// Top-level sessions: ID is the filename stem; ParentSessionID is "".
// Subagent sessions: layout is
// <root>/<projectDir>/<parentSessionId>/subagents/agent-<agentId>.jsonl.
// ID becomes "<parentSessionId>#<agentId>"; ParentSessionID is
// <parentSessionId>. The "agent-" prefix is stripped if present.
func composeID(absPath string, isSubagent bool) (id, parentID string) {
	base := filepath.Base(absPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	if !isSubagent {
		return stem, ""
	}

	// Walk up: <project>/<parentSessionId>/subagents/<file>
	subagentsDir := filepath.Dir(absPath)
	parentDir := filepath.Dir(subagentsDir) // <parentSessionId>
	parentID = filepath.Base(parentDir)
	agentID := strings.TrimPrefix(stem, "agent-")
	return parentID + "#" + agentID, parentID
}

// sortSessions returns sessions sorted by StartedAt descending, with
// ID ascending as the deterministic tiebreaker. Returns a new slice;
// the input is not modified.
func sortSessions(in []*Session) []*Session {
	out := make([]*Session, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// buildCwdIndex flattens sessions into (cwd, sessionID) rows sorted by
// cwd ascending, ready for binary-search prefix lookup.
func buildCwdIndex(sessions []*Session) []cwdIndexEntry {
	var rows []cwdIndexEntry
	for _, s := range sessions {
		for _, cwd := range s.Cwds {
			if cwd == "" {
				continue
			}
			rows = append(rows, cwdIndexEntry{Cwd: cwd, SessionID: s.ID})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Cwd != rows[j].Cwd {
			return rows[i].Cwd < rows[j].Cwd
		}
		return rows[i].SessionID < rows[j].SessionID
	})
	return rows
}

// Close releases the fs watcher and any background goroutines; live
// Tail channels created against this Store are closed.
//
// Safe to call multiple times. In v1 there is no watcher and no
// background work yet, so Close only marks the store closed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// Sessions returns all indexed sessions, ordered by StartedAt
// descending with ID as the deterministic tiebreaker.
// Returned pointers are stable index entries; do not mutate.
func (s *Store) Sessions() iter.Seq[*Session] {
	return func(yield func(*Session) bool) {
		s.mu.RLock()
		// Snapshot the slice header to avoid holding the lock across
		// the consumer's body.
		snapshot := s.ordered
		s.mu.RUnlock()
		for _, sess := range snapshot {
			if !yield(sess) {
				return
			}
		}
	}
}

// Session looks up one session by ID. For subagents, the ID is
// "<parentSessionId>#<agentId>". Returns ErrNotFound (wrappable via
// errors.Is) if no such session is indexed.
func (s *Store) Session(id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return sess, nil
}

// Query enumerates sessions matching the filter. Zero-valued fields on
// Query are wildcards. Order matches Sessions() (StartedAt descending,
// ID ascending as tiebreaker).
func (s *Store) Query(q Query) iter.Seq[*Session] {
	since := q.Since
	until := q.Until
	cwdPrefix := q.CwdPrefix
	model := q.Model

	return func(yield func(*Session) bool) {
		s.mu.RLock()
		snapshot := s.ordered
		s.mu.RUnlock()

		for _, sess := range snapshot {
			if !matches(sess, cwdPrefix, since, until, model) {
				continue
			}
			if !yield(sess) {
				return
			}
		}
	}
}

// matches applies the Query filters to a single session.
func matches(sess *Session, cwdPrefix string, since, until time.Time, model string) bool {
	// Cwd prefix: match against ANY entry in Cwds.
	if cwdPrefix != "" {
		ok := false
		for _, c := range sess.Cwds {
			if strings.HasPrefix(c, cwdPrefix) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	// Since/Until bracket Session.StartedAt. Zero values are unbounded.
	if !since.IsZero() && sess.StartedAt.Before(since) {
		return false
	}
	if !until.IsZero() && sess.StartedAt.After(until) {
		return false
	}
	// Model substring (case-insensitive).
	if !containsFoldSubstring(sess.Model, model) {
		return false
	}
	return true
}

// SessionsByCwdPrefix is the aggregator's hot-path correlation lookup:
// every indexed session whose Cwds contains an entry with the given
// prefix. Backed by an in-memory sorted (cwd, sessionId) slice with
// binary search; O(log n + matches) per call. The returned slice is
// ordered by StartedAt descending (matching Sessions) and deduped
// across multiple matching cwd entries on the same session.
func (s *Store) SessionsByCwdPrefix(prefix string) []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if prefix == "" {
		// Empty prefix would match every entry; surface every
		// session (deduped, ordered) as a convenience rather than
		// returning the raw cwdIndex.
		out := make([]*Session, len(s.ordered))
		copy(out, s.ordered)
		return out
	}

	// Locate the first cwdIndex entry with Cwd >= prefix.
	lo := sort.Search(len(s.cwdIndex), func(i int) bool {
		return s.cwdIndex[i].Cwd >= prefix
	})

	seen := make(map[string]struct{})
	for i := lo; i < len(s.cwdIndex); i++ {
		row := s.cwdIndex[i]
		if !strings.HasPrefix(row.Cwd, prefix) {
			break
		}
		seen[row.SessionID] = struct{}{}
	}

	if len(seen) == 0 {
		return nil
	}

	out := make([]*Session, 0, len(seen))
	for _, sess := range s.ordered {
		if _, ok := seen[sess.ID]; ok {
			out = append(out, sess)
		}
	}
	return out
}

// Events iterates the events of a session in file order. Malformed
// lines surface as a non-nil error from the Seq2; iteration continues
// after the receiver consumes the error, so the caller controls
// termination via yield's return value.
//
// Filtering rules (per docs/jsonl-schema.md and the locked decision in
// docs/sessions-interface.md "Open questions" Q1):
//   - isMeta:true lines (at either the top level or inside message)
//     are skipped silently.
//   - Side-band types (attachment, file-history-snapshot, all meta line
//     types) are skipped silently.
//   - system lines are skipped unless subtype=="compact_boundary", in
//     which case they emit EventCompactBoundary.
//   - assistant lines emit EventAssistant followed by one EventToolUse
//     per tool_use content block (in observation order).
//   - user lines whose content includes any tool_result block emit one
//     EventToolResult per tool_result block instead of EventUser.
//
// Order is file order. Timestamps are NOT used for ordering; the
// schema doc notes adjacent pairs invert in ~1-2% of cases.
//
// For subagent files, Event.SessionID carries the composite indexed ID
// ("<parent>#<agentId>"). Unknown sessionID yields a single
// (Event{}, ErrNotFound) pair then stops.
func (s *Store) Events(sessionID string) iter.Seq2[Event, error] {
	return s.events(sessionID)
}

// Tail and TailStats are implemented in tail.go.
