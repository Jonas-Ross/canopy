package sessions

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// inflightHydrate tracks a Hydrate call currently in progress for a
// session ID. Concurrent callers find the same value via the
// per-Store sync.Map and block on done; only the first caller runs
// the underlying file scan. When the scan finishes the value is
// removed from the map, so the next sequential call performs a fresh
// scan (preserving the documented idempotence-via-re-scan contract).
type inflightHydrate struct {
	done   chan struct{}   // closed when the scan completes
	result hydrateResult   // valid iff err == nil
	err    error           // non-nil → scan failed
}

// storeGates holds the per-Store in-flight tables. Keyed by *Store;
// value is a *sync.Map keyed by session ID → *inflightHydrate. Kept
// in a package-level sync.Map so this file owns it entirely (the
// brief restricts edits to store.go).
var storeGates sync.Map // map[*Store]*sync.Map

// Hydrate fills the lazy fields on Session: EventCount, Tokens, Tools,
// Meta, and the full Cwds set. Idempotent: each call re-scans the
// JSONL and overwrites — repeated calls converge on the same final
// state. The per-session single-flight ensures concurrent callers
// share one scan rather than racing on the file.
//
// Behavior:
//   - sess == nil or sess.ID == "" returns an error.
//   - A session ID not present in the index returns a wrapped
//     ErrNotFound.
//   - The session's JSONL file is scanned line by line. Malformed
//     lines fail Hydrate fast (Hydrate is about accurate aggregation,
//     not best-effort rendering).
//   - EventCount counts every event surfaced by the same classifier
//     Events() uses (post-meta / side-band filtering).
//   - Tokens are summed across assistant lines deduped by message.id:
//     the CLI splits one logical assistant response across multiple
//     JSONL lines that all carry the same message.id and the same
//     usage object. Counting each line would double- or triple-count.
//   - Tools[name] counts EventToolUse events. Tool results are not
//     calls and are not counted. After Hydrate, Tools is always
//     non-nil (possibly empty).
//   - Cwds is replaced with the full ordered distinct-cwd set
//     observed across user lines. Observation order is preserved; no
//     sorting.
//   - CompactBoundary events contribute to EventCount only; their
//     PreCompactTokens / PostCompactTokens are scratch reference
//     (real per-message accounting lives on assistant lines).
//   - Meta is folded from JSONL meta lines (last-prompt, ai-title,
//     permission-mode, agent-name, custom-title, agent-setting,
//     pr-link, worktree-state, queue-operation) with last-write-wins
//     semantics. Meta lines do not count toward EventCount. The
//     Events() contract is unchanged — they are still filtered there.
//
// Concurrency contract:
//   - Concurrent Hydrate calls for the same session collapse into one
//     file scan: the first caller runs the scan, later concurrent
//     callers block on the same in-flight record and reuse its
//     result. Sequential calls each re-scan.
//   - The final state is installed onto the indexed Session under a
//     short write lock; concurrent readers of *sess see either the
//     pre-hydrate or post-hydrate snapshot, never a partial mix.
//     EventCount / Tokens / Tools / Cwds are committed together.
//   - Distinct sessions hydrate in parallel.
func (s *Store) Hydrate(sess *Session) error {
	if sess == nil {
		return errors.New("sessions: Hydrate: nil session")
	}
	if sess.ID == "" {
		return errors.New("sessions: Hydrate: empty session ID")
	}

	// Confirm the session is in the index. Hydrate writes through to
	// the index entry; an unindexed *Session is a caller bug.
	indexed, err := s.Session(sess.ID)
	if err != nil {
		return fmt.Errorf("sessions: Hydrate %s: %w", sess.ID, err)
	}

	// Single-flight: either start a new in-flight or attach to an
	// existing one. The starter is the goroutine whose LoadOrStore
	// inserted the fresh record; only it runs the scan.
	gatesAny, _ := storeGates.LoadOrStore(s, &sync.Map{})
	gates := gatesAny.(*sync.Map)

	mine := &inflightHydrate{done: make(chan struct{})}
	got, loaded := gates.LoadOrStore(sess.ID, mine)
	flight := got.(*inflightHydrate)

	if hook := hydrateGateHook.Load(); hook != nil {
		(*hook)(sess.ID, loaded)
	}

	if loaded {
		// Someone else is scanning; wait for them and reuse.
		<-flight.done
		if flight.err != nil {
			return flight.err
		}
		commitHydrate(s, indexed, sess, flight.result)
		return nil
	}

	// We are the scanner. Always remove the in-flight record and
	// close done so blocked waiters unblock exactly once.
	defer func() {
		gates.Delete(sess.ID)
		close(flight.done)
	}()

	if hook := hydrateScanHook.Load(); hook != nil {
		(*hook)(sess.ID)
	}

	result, err := computeHydrate(s, sess.ID)
	if err != nil {
		flight.err = err
		return err
	}
	flight.result = result

	commitHydrate(s, indexed, sess, result)
	return nil
}

// commitHydrate installs result onto both the indexed Session
// (canonical, stable pointer) and the caller's *sess (which may be
// distinct on test paths). The Store's RWMutex serialises this with
// other readers / writers so observers never see a partial mix.
func commitHydrate(s *Store, indexed, sess *Session, result hydrateResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	indexed.EventCount = result.eventCount
	indexed.Tokens = result.tokens
	indexed.Tools = result.tools
	indexed.Cwds = result.cwds
	indexed.Meta = result.meta

	if sess != indexed {
		sess.EventCount = result.eventCount
		sess.Tokens = result.tokens
		sess.Tools = result.tools
		sess.Cwds = result.cwds
		sess.Meta = result.meta
	}
}

// hydrateScanHook is a test-only seam invoked at the start of every
// underlying file scan (exactly once per single-flight). Tests
// override it via atomic.Pointer to assert single-flight behaviour
// without re-scanning. Production callers never set it; nil load is
// the no-op fast path.
var hydrateScanHook atomic.Pointer[func(sessionID string)]

// hydrateGateHook is a test-only seam invoked once per Hydrate call,
// right after LoadOrStore decides whether the caller is the scanner
// (loaded=false) or a follower (loaded=true). Tests use it to
// synchronise on "all N goroutines have entered the gate" before
// releasing the scanner, eliminating timing races in single-flight
// assertions.
var hydrateGateHook atomic.Pointer[func(sessionID string, follower bool)]

// hydrateResult is the intermediate value computed during a scan
// before it is committed onto the indexed Session.
type hydrateResult struct {
	eventCount int
	tokens     TokenStats
	tools      map[string]int
	cwds       []string
	meta       SessionMeta
}

// computeHydrate scans the session's JSONL file once and returns the
// aggregated event totals plus the folded SessionMeta. It does not
// mutate any *Session; the caller commits the result under the
// per-session gate.
//
// One scan handles both event and meta classification: each non-blank
// line is fed to eventsFromLine first (the common case is an event
// line); lines that surface no events are tried via metaFromLine so
// the meta state lands in the same hydrateResult.
func computeHydrate(s *Store, sessionID string) (hydrateResult, error) {
	sess, err := s.Session(sessionID)
	if err != nil {
		return hydrateResult{}, fmt.Errorf("sessions: hydrate %s: %w", sessionID, err)
	}

	f, err := os.Open(sess.Path)
	if err != nil {
		return hydrateResult{}, fmt.Errorf("sessions: hydrate %s: open %s: %w", sessionID, sess.Path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanLineSize)

	res := hydrateResult{
		tools: make(map[string]int),
	}
	seenMessageIDs := make(map[string]struct{})
	seenCwds := make(map[string]struct{})

	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Bytes()
		if isBlank(line) {
			continue
		}

		buf := make([]byte, len(line))
		copy(buf, line)

		events, perr := eventsFromLine(buf, sess.ID)
		if perr != nil {
			return hydrateResult{}, fmt.Errorf("sessions: hydrate %s: malformed line %d in %s: %w", sessionID, lineNum, sess.Path, perr)
		}

		if len(events) > 0 {
			for _, ev := range events {
				foldEvent(&res, ev, seenMessageIDs, seenCwds)
			}
			continue
		}

		// No events surfaced. Could be a meta line we want to fold,
		// or a filtered side-band / isMeta line — metaFromLine
		// returns false for the latter.
		if mu, ok, _ := metaFromLine(buf); ok {
			applyMetaUpdate(&res.meta, mu)
		}
	}
	if err := sc.Err(); err != nil {
		return hydrateResult{}, fmt.Errorf("sessions: hydrate %s: scan %s: %w", sessionID, sess.Path, err)
	}

	return res, nil
}

// foldEvent applies one Event to the running hydrateResult. Split out
// of computeHydrate's hot loop so the scan body stays scannable.
func foldEvent(res *hydrateResult, ev Event, seenMessageIDs, seenCwds map[string]struct{}) {
	res.eventCount++

	switch ev.Kind {
	case EventAssistant:
		if ev.Assistant == nil {
			return
		}
		if id := ev.Assistant.MessageID; id != "" {
			if _, dup := seenMessageIDs[id]; dup {
				return
			}
			seenMessageIDs[id] = struct{}{}
		}
		// Assistant lines without a MessageID are treated as their
		// own bucket (one-shot accounting); we cannot dedupe what the
		// CLI did not key. In practice this is vanishingly rare on
		// real data.
		res.tokens.Input += ev.Assistant.Tokens.Input
		res.tokens.Output += ev.Assistant.Tokens.Output
		res.tokens.CacheRead += ev.Assistant.Tokens.CacheRead
		res.tokens.CacheCreation += ev.Assistant.Tokens.CacheCreation

	case EventToolUse:
		if ev.ToolUse == nil {
			return
		}
		res.tools[ev.ToolUse.Name]++

	case EventUser:
		if ev.User == nil {
			return
		}
		cwd := ev.User.Cwd
		if cwd == "" {
			return
		}
		if _, dup := seenCwds[cwd]; !dup {
			seenCwds[cwd] = struct{}{}
			res.cwds = append(res.cwds, cwd)
		}

	case EventToolResult, EventCompactBoundary:
		// Counted in EventCount above; nothing else to aggregate.
	}
}
