package sessions

import (
	"reflect"
	"testing"
)

// hydrateMetaFixture mixes one minimal conversation turn (so Open()
// indexes the session) with one of every meta line type modeled on
// SessionMeta, plus:
//   - a repeat last-prompt and permission-mode to prove last-write-wins
//   - an unknown meta type ("does-not-exist") to prove forward-compat
//   - a file-history-snapshot line that must be ignored, not folded
//   - an isMeta:true synthetic user line that must not count or surface
//
// Field names match production JSONL sampled from ~/.claude/projects
// (see docs/jsonl-schema.md §3).
const hydrateMetaFixture = `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"hydrate-meta","timestamp":"2026-05-15T10:00:00.000Z","cwd":"/work/m","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","sessionId":"hydrate-meta","timestamp":"2026-05-15T10:00:01.000Z","cwd":"/work/m","gitBranch":"main","version":"2.1.143","message":{"id":"msg_x","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"reply"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req_x"}
{"type":"last-prompt","lastPrompt":"earlier prompt","leafUuid":"u-0","sessionId":"hydrate-meta"}
{"type":"ai-title","aiTitle":"Final AI title","sessionId":"hydrate-meta"}
{"type":"custom-title","customTitle":"my-custom-title","sessionId":"hydrate-meta"}
{"type":"permission-mode","permissionMode":"plan","sessionId":"hydrate-meta"}
{"type":"agent-name","agentName":"ship-something","sessionId":"hydrate-meta"}
{"type":"agent-setting","agentSetting":"claude","sessionId":"hydrate-meta"}
{"type":"pr-link","sessionId":"hydrate-meta","prNumber":42,"prUrl":"https://github.com/jonas/canopy/pull/42","prRepository":"jonas/canopy","timestamp":"2026-05-15T10:00:02.000Z"}
{"type":"worktree-state","worktreeSession":{"originalCwd":"/work/m","worktreePath":"/work/m/.wt/feat","worktreeName":"feat","worktreeBranch":"worktree-feat","originalBranch":"main","originalHeadCommit":"abc123","sessionId":"hydrate-meta"},"sessionId":"hydrate-meta"}
{"type":"queue-operation","operation":"enqueue","timestamp":"2026-05-15T10:00:03.000Z","sessionId":"hydrate-meta","content":"queued prompt body"}
{"type":"file-history-snapshot","snapshot":{"files":[]},"sessionId":"hydrate-meta"}
{"type":"does-not-exist","value":"forward-compat","sessionId":"hydrate-meta"}
{"type":"last-prompt","lastPrompt":"the latest prompt","leafUuid":"u-z","sessionId":"hydrate-meta"}
{"type":"permission-mode","permissionMode":"auto","sessionId":"hydrate-meta"}
{"type":"user","uuid":"u-meta","parentUuid":"a-1","sessionId":"hydrate-meta","timestamp":"2026-05-15T10:00:04.000Z","cwd":"/work/m","gitBranch":"main","version":"2.1.143","isMeta":true,"message":{"role":"user","content":"/some-command output"}}
`

// hydrateMetaWantMeta is the SessionMeta the fixture above should yield.
// Last-write-wins on LastPrompt and PermissionMode: "the latest prompt"
// and "auto" win over their earlier values.
var hydrateMetaWantMeta = SessionMeta{
	LastPrompt:     "the latest prompt",
	AITitle:        "Final AI title",
	CustomTitle:    "my-custom-title",
	PermissionMode: "auto",
	AgentName:      "ship-something",
	AgentSetting:   "claude",
	PRLink: PRLinkMeta{
		Number:     42,
		URL:        "https://github.com/jonas/canopy/pull/42",
		Repository: "jonas/canopy",
	},
	WorktreeState: WorktreeStateMeta{
		OriginalCwd:        "/work/m",
		WorktreePath:       "/work/m/.wt/feat",
		WorktreeName:       "feat",
		WorktreeBranch:     "worktree-feat",
		OriginalBranch:     "main",
		OriginalHeadCommit: "abc123",
	},
	QueueOperation: QueueOpMeta{
		Operation: "enqueue",
		Content:   "queued prompt body",
	},
}

func TestHydrate_Meta_PopulatedAllFields(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-m", "hydrate-meta", hydrateMetaFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}

	if !reflect.DeepEqual(sess.Meta, hydrateMetaWantMeta) {
		t.Errorf("Meta=%+v\nwant   %+v", sess.Meta, hydrateMetaWantMeta)
	}
}

func TestHydrate_Meta_LastWriteWins(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-m", "hydrate-meta", hydrateMetaFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}

	if got, want := sess.Meta.LastPrompt, "the latest prompt"; got != want {
		t.Errorf("LastPrompt=%q, want %q (earlier value leaked)", got, want)
	}
	if got, want := sess.Meta.PermissionMode, "auto"; got != want {
		t.Errorf("PermissionMode=%q, want %q (earlier value leaked)", got, want)
	}
}

func TestHydrate_Meta_NotInEventCount(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-m", "hydrate-meta", hydrateMetaFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}

	// Only the two real conversation lines (u-1 user, a-1 assistant) are
	// events. The isMeta:true user line, all meta lines, the
	// file-history-snapshot line, and the unknown type must not count.
	if got, want := sess.EventCount, 2; got != want {
		t.Errorf("EventCount=%d, want %d (meta lines leaked into events)", got, want)
	}
}

// hydrateNoMetaFixture has conversation but zero meta lines.
const hydrateNoMetaFixture = `{"type":"user","uuid":"u-1","parentUuid":null,"sessionId":"hydrate-nometa","timestamp":"2026-05-15T10:00:00.000Z","cwd":"/work/n","gitBranch":"main","version":"2.1.143","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","sessionId":"hydrate-nometa","timestamp":"2026-05-15T10:00:01.000Z","cwd":"/work/n","gitBranch":"main","version":"2.1.143","message":{"id":"msg_only","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"reply"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"requestId":"req"}
`

func TestHydrate_Meta_ZeroWhenAbsent(t *testing.T) {
	root, id, _ := writeHydrateFixture(t, "-work-n", "hydrate-nometa", hydrateNoMetaFixture)
	store := openWith(t, root)

	sess, err := store.Session(id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := store.Hydrate(sess); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}

	var zero SessionMeta
	if !reflect.DeepEqual(sess.Meta, zero) {
		t.Errorf("Meta=%+v, want zero value", sess.Meta)
	}
}

func TestHydrate_Meta_EventsContractUnchanged(t *testing.T) {
	// Events() must still hide every meta line. We sanity-check this on
	// the meta fixture by confirming Events surfaces exactly the two
	// conversation events (the isMeta:true user line is also hidden).
	root, id, _ := writeHydrateFixture(t, "-work-m", "hydrate-meta", hydrateMetaFixture)
	store := openWith(t, root)

	var count int
	for ev, err := range store.Events(id) {
		if err != nil {
			t.Fatalf("Events err: %v", err)
		}
		switch ev.Kind {
		case EventUser, EventAssistant:
			count++
		default:
			t.Errorf("unexpected event kind %v surfaced", ev.Kind)
		}
	}
	if got, want := count, 2; got != want {
		t.Errorf("conversation events=%d, want %d (meta line leaked into Events)", got, want)
	}
}
