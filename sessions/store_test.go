package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// testdataRoot is the static fixture root. Tests that need mtime
// control materialize fixtures in t.TempDir() instead.
const testdataRoot = "testdata/projects"

// expected session IDs in the static fixture tree.
const (
	idHappy    = "00000001-0000-0000-0000-000000000001"
	idMulti    = "00000002-0000-0000-0000-000000000002"
	idAgent    = "00000003-0000-0000-0000-000000000003"
	idSubagent = "00000003-0000-0000-0000-000000000003#abc123"
	idOnlyhere = "00000005-0000-0000-0000-000000000005"
)

// expectedTopLevelIDs is the set of session IDs that should be in the
// index after Open on the static fixture root, including the composite
// subagent ID and excluding the meta-only file.
var expectedAllIDs = []string{idHappy, idMulti, idAgent, idSubagent, idOnlyhere}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(testdataRoot)
	if err != nil {
		t.Fatalf("Open(%q): %v", testdataRoot, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func collectIDs(store *Store) []string {
	var ids []string
	for s := range store.Sessions() {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestOpen_DiscoversTopLevelAndSubagentSessions(t *testing.T) {
	store := openTestStore(t)

	got := collectIDs(store)
	sort.Strings(got)

	want := append([]string(nil), expectedAllIDs...)
	sort.Strings(want)

	if !stringSlicesEqual(got, want) {
		t.Fatalf("session IDs mismatch:\n got: %v\nwant: %v", got, want)
	}

	// Subagent linkage assertions.
	sub, err := store.Session(idSubagent)
	if err != nil {
		t.Fatalf("Session(%q): %v", idSubagent, err)
	}
	if !sub.IsSidechain {
		t.Errorf("subagent IsSidechain=false, want true")
	}
	if sub.ParentSessionID != idAgent {
		t.Errorf("subagent ParentSessionID=%q, want %q", sub.ParentSessionID, idAgent)
	}

	parent, err := store.Session(idAgent)
	if err != nil {
		t.Fatalf("Session(%q): %v", idAgent, err)
	}
	if parent.IsSidechain {
		t.Errorf("parent IsSidechain=true, want false")
	}
	if parent.ParentSessionID != "" {
		t.Errorf("parent ParentSessionID=%q, want empty", parent.ParentSessionID)
	}
}

func TestOpen_SkipsMetaOnlySessions(t *testing.T) {
	store := openTestStore(t)
	for s := range store.Sessions() {
		if strings.HasSuffix(s.Path, "00000004-0000-0000-0000-000000000004.jsonl") {
			t.Fatalf("meta-only session was indexed: %s", s.Path)
		}
		if s.ID == "00000004-0000-0000-0000-000000000004" {
			t.Fatalf("meta-only session present by ID: %s", s.ID)
		}
	}
}

func TestOpen_PopulatesCwdsFirstAndLast(t *testing.T) {
	store := openTestStore(t)

	happy, err := store.Session(idHappy)
	if err != nil {
		t.Fatalf("Session(happy): %v", err)
	}
	if !stringSlicesEqual(happy.Cwds, []string{"/tmp/projects/happy"}) {
		t.Errorf("happy.Cwds=%v, want [/tmp/projects/happy]", happy.Cwds)
	}

	multi, err := store.Session(idMulti)
	if err != nil {
		t.Fatalf("Session(multi): %v", err)
	}
	if !stringSlicesEqual(multi.Cwds, []string{"/tmp/projects/multi", "/tmp/projects/multi/sub"}) {
		t.Errorf("multi.Cwds=%v, want [/tmp/projects/multi /tmp/projects/multi/sub]", multi.Cwds)
	}
}

func TestOpen_PopulatesModelAndTimestamps(t *testing.T) {
	// Build a tiny dynamic fixture under TempDir so we can pin mtime
	// with os.Chtimes and verify UpdatedAt = file mtime.
	root := t.TempDir()
	proj := filepath.Join(root, "-tmp-projects-dyn")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(proj, "dyn00000-0000-0000-0000-000000000001.jsonl")
	content := `{"type":"user","uuid":"dyn-1","parentUuid":null,"sessionId":"dyn00000-0000-0000-0000-000000000001","timestamp":"2026-05-10T12:00:00.000Z","cwd":"/tmp/dyn","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"dyn-2","parentUuid":"dyn-1","sessionId":"dyn00000-0000-0000-0000-000000000001","timestamp":"2026-05-10T12:00:01.000Z","cwd":"/tmp/dyn","message":{"id":"m","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`
	if err := os.WriteFile(jsonl, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	wantMtime := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(jsonl, wantMtime, wantMtime); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open(%q): %v", root, err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sess, err := store.Session("dyn00000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatalf("Session(dyn): %v", err)
	}

	wantStarted := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	if !sess.StartedAt.Equal(wantStarted) {
		t.Errorf("StartedAt=%v, want %v", sess.StartedAt, wantStarted)
	}
	if !sess.UpdatedAt.Equal(wantMtime) {
		t.Errorf("UpdatedAt=%v, want %v (file mtime)", sess.UpdatedAt, wantMtime)
	}
	if sess.Model != "claude-opus-4-7" {
		t.Errorf("Model=%q, want claude-opus-4-7", sess.Model)
	}
}

func TestSession_NotFoundReturnsSentinel(t *testing.T) {
	store := openTestStore(t)
	_, err := store.Session("definitely-not-an-id")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestSessions_OrderedByStartedAtDescending(t *testing.T) {
	store := openTestStore(t)
	var got []time.Time
	for s := range store.Sessions() {
		got = append(got, s.StartedAt)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Before(got[i]) {
			t.Errorf("sessions not in descending order: index %d (%v) is before index %d (%v)", i-1, got[i-1], i, got[i])
		}
	}
	// Spot-check: onlyhere (2026-05-04) is newer than happy (2026-05-01).
	idsInOrder := collectIDs(store)
	posOnlyhere := indexOf(idsInOrder, idOnlyhere)
	posHappy := indexOf(idsInOrder, idHappy)
	if posOnlyhere == -1 || posHappy == -1 {
		t.Fatalf("expected ids not present: %v", idsInOrder)
	}
	if posOnlyhere >= posHappy {
		t.Errorf("onlyhere (newer) should come before happy (older); got order %v", idsInOrder)
	}
}

func TestQuery_FiltersByCwdPrefix(t *testing.T) {
	store := openTestStore(t)

	// Exact match.
	gotIDs := queryIDs(store, Query{CwdPrefix: "/tmp/projects/happy"})
	if !stringSlicesEqual(gotIDs, []string{idHappy}) {
		t.Errorf("exact: got %v want [%s]", gotIDs, idHappy)
	}

	// Prefix match: /tmp/projects/multi matches both /tmp/projects/multi and /tmp/projects/multi/sub.
	gotIDs = queryIDs(store, Query{CwdPrefix: "/tmp/projects/multi"})
	if !stringSlicesEqual(gotIDs, []string{idMulti}) {
		t.Errorf("prefix multi: got %v want [%s]", gotIDs, idMulti)
	}

	// No match.
	gotIDs = queryIDs(store, Query{CwdPrefix: "/no/such/path"})
	if len(gotIDs) != 0 {
		t.Errorf("no-match: got %v want empty", gotIDs)
	}
}

func TestQuery_FiltersBySinceUntil(t *testing.T) {
	store := openTestStore(t)

	// Unbounded: everything.
	all := queryIDs(store, Query{})
	if len(all) != len(expectedAllIDs) {
		t.Errorf("unbounded: got %d sessions, want %d", len(all), len(expectedAllIDs))
	}

	// Since = 2026-05-03: agent, subagent, onlyhere.
	since := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
	got := queryIDs(store, Query{Since: since})
	wantSet := map[string]bool{idAgent: true, idSubagent: true, idOnlyhere: true}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("since: unexpected id %s", id)
		}
	}
	if len(got) != len(wantSet) {
		t.Errorf("since: got %v want %v", got, wantSet)
	}

	// Until = 2026-05-02T23:59:59Z: happy + multi.
	until := time.Date(2026, 5, 2, 23, 59, 59, 0, time.UTC)
	got = queryIDs(store, Query{Until: until})
	wantSet = map[string]bool{idHappy: true, idMulti: true}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("until: unexpected id %s", id)
		}
	}
	if len(got) != len(wantSet) {
		t.Errorf("until: got %v want %v", got, wantSet)
	}

	// Bounded range: 2026-05-02 .. 2026-05-03T23:59:59Z = multi + agent + subagent.
	got = queryIDs(store, Query{
		Since: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 5, 3, 23, 59, 59, 0, time.UTC),
	})
	wantSet = map[string]bool{idMulti: true, idAgent: true, idSubagent: true}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("range: unexpected id %s", id)
		}
	}
	if len(got) != len(wantSet) {
		t.Errorf("range: got %v want %v", got, wantSet)
	}
}

func TestQuery_FiltersByModelSubstring(t *testing.T) {
	store := openTestStore(t)

	// "opus" → happy + agent + subagent (lowercase matches "claude-opus-4-7").
	got := queryIDs(store, Query{Model: "opus"})
	wantSet := map[string]bool{idHappy: true, idAgent: true, idSubagent: true}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("opus: unexpected id %s", id)
		}
	}
	if len(got) != len(wantSet) {
		t.Errorf("opus: got %v want %v", got, wantSet)
	}

	// "SONNET" (uppercase) matches "claude-sonnet-4-6" — case-insensitive.
	got = queryIDs(store, Query{Model: "SONNET"})
	if !stringSlicesEqual(got, []string{idMulti}) {
		t.Errorf("SONNET: got %v want [%s]", got, idMulti)
	}

	// "haiku" matches just onlyhere.
	got = queryIDs(store, Query{Model: "haiku"})
	if !stringSlicesEqual(got, []string{idOnlyhere}) {
		t.Errorf("haiku: got %v want [%s]", got, idOnlyhere)
	}
}

func TestSessionsByCwdPrefix_FastLookup(t *testing.T) {
	store := openTestStore(t)

	// Same prefix that Query{CwdPrefix} uses; results must agree.
	for _, prefix := range []string{
		"/tmp/projects/happy",
		"/tmp/projects/multi",
		"/tmp/projects/multi/sub",
		"/tmp/projects/agent-test",
		"/tmp/projects/onlyhere",
		"/no/such/path",
	} {
		fast := idsFromSlice(store.SessionsByCwdPrefix(prefix))
		slow := queryIDs(store, Query{CwdPrefix: prefix})
		sort.Strings(fast)
		sort.Strings(slow)
		if !stringSlicesEqual(fast, slow) {
			t.Errorf("prefix %q: fast=%v slow=%v", prefix, fast, slow)
		}
	}

	// Cross-project: a prefix that matches only one project.
	got := idsFromSlice(store.SessionsByCwdPrefix("/tmp/projects/onlyhere"))
	if !stringSlicesEqual(got, []string{idOnlyhere}) {
		t.Errorf("onlyhere prefix: got %v want [%s]", got, idOnlyhere)
	}
}

func TestSessionsByCwdPrefix_DedupesAcrossCwds(t *testing.T) {
	store := openTestStore(t)
	// /tmp/projects/multi matches BOTH cwds of the multi session
	// (/tmp/projects/multi and /tmp/projects/multi/sub). The session
	// must appear once.
	got := store.SessionsByCwdPrefix("/tmp/projects/multi")
	if len(got) != 1 {
		t.Fatalf("expected 1 deduped session, got %d (%v)", len(got), idsFromSlice(got))
	}
	if got[0].ID != idMulti {
		t.Errorf("got id %q want %q", got[0].ID, idMulti)
	}
}

func TestStore_ConcurrentRead_RaceClean(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)

	var wg sync.WaitGroup
	const workers = 8
	const iters = 50
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = collectIDs(store)
				_ = queryIDs(store, Query{Model: "opus"})
				_ = store.SessionsByCwdPrefix("/tmp/projects/multi")
				if _, err := store.Session(idHappy); err != nil {
					t.Errorf("Session(idHappy) err=%v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// --- helpers ---

func queryIDs(store *Store, q Query) []string {
	var ids []string
	for s := range store.Query(q) {
		ids = append(ids, s.ID)
	}
	return ids
}

func idsFromSlice(xs []*Session) []string {
	out := make([]string, 0, len(xs))
	for _, s := range xs {
		out = append(out, s.ID)
	}
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
