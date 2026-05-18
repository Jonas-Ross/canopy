package git

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// cmdKey identifies a git subcommand by its first two non-flag args
// after the `-C <path>` prefix. The fakeRunner uses it to dispatch
// stubbed responses without having to match the full argv.
type cmdKey string

const (
	keyWorktreeList   cmdKey = "worktree list"
	keySymbolicRef    cmdKey = "symbolic-ref"
	keyStatus         cmdKey = "status"
	keyRevList        cmdKey = "rev-list"
	keyLog            cmdKey = "log"
)

// fakeResponse is one canned reply the fake runner will return when it
// matches the request key.
type fakeResponse struct {
	out []byte
	err error
}

// installFakeRunner replaces the package-level runCmd with a dispatcher
// driven by responses keyed on cmdKey. Restores the original on cleanup.
func installFakeRunner(t *testing.T, responses map[cmdKey]fakeResponse) {
	t.Helper()
	orig := setRunCmd(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" {
			t.Fatalf("unexpected binary %q", name)
		}
		key := classify(args)
		resp, ok := responses[key]
		if !ok {
			t.Fatalf("no fake response registered for %q (args=%v)", key, args)
		}
		return resp.out, resp.err
	})
	t.Cleanup(func() { setRunCmd(orig) })
}

// classify reduces an argv to the cmdKey by skipping the leading
// `-C <path>` pair (when present) and returning the next one or two
// tokens that identify the subcommand.
func classify(args []string) cmdKey {
	i := 0
	if len(args) >= 2 && args[0] == "-C" {
		i = 2
	}
	if i >= len(args) {
		return ""
	}
	head := args[i]
	switch head {
	case "worktree":
		// `worktree list` is the only subcommand we issue.
		return keyWorktreeList
	case "symbolic-ref":
		return keySymbolicRef
	case "status":
		return keyStatus
	case "rev-list":
		return keyRevList
	case "log":
		return keyLog
	}
	return cmdKey(head)
}

func TestListWorktrees_ParsesPorcelain(t *testing.T) {
	fixture := "" +
		"worktree /repo/main\n" +
		"HEAD abc1234\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /repo/bare-mirror\n" +
		"bare\n" +
		"\n" +
		"worktree /repo/detached\n" +
		"HEAD def5678\n" +
		"detached\n" +
		"\n"

	installFakeRunner(t, map[cmdKey]fakeResponse{
		keyWorktreeList: {out: []byte(fixture)},
	})

	got, err := ListWorktrees(context.Background(), "/repo/main")
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	want := []Worktree{
		{Path: "/repo/main", Branch: "main"},
		{Path: "/repo/bare-mirror", Bare: true},
		{Path: "/repo/detached", Detached: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListWorktrees: got %+v want %+v", got, want)
	}
}

func TestListWorktrees_EmptyRepo(t *testing.T) {
	// A freshly initialized repo with no extra worktrees still reports
	// the main worktree.
	fixture := "" +
		"worktree /repo/only\n" +
		"HEAD 0000000\n" +
		"branch refs/heads/main\n" +
		"\n"

	installFakeRunner(t, map[cmdKey]fakeResponse{
		keyWorktreeList: {out: []byte(fixture)},
	})

	got, err := ListWorktrees(context.Background(), "/repo/only")
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	want := []Worktree{{Path: "/repo/only", Branch: "main"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListWorktrees: got %+v want %+v", got, want)
	}
}

func TestListWorktrees_PropagatesRunError(t *testing.T) {
	installFakeRunner(t, map[cmdKey]fakeResponse{
		keyWorktreeList: {err: errors.New("exit status 128: fatal: not a git repository")},
	})
	_, err := ListWorktrees(context.Background(), "/nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "git: list worktrees:") {
		t.Errorf("error not wrapped as expected: %v", err)
	}
}

// happyPathResponses is the canned reply set for a healthy worktree
// (attached branch, three dirty files, two commits ahead, one behind,
// upstream configured, a populated last commit).
func happyPathResponses() map[cmdKey]fakeResponse {
	return map[cmdKey]fakeResponse{
		keySymbolicRef: {out: []byte("feat/things\n")},
		keyStatus: {out: []byte("" +
			" M file_one.go\n" +
			"?? new.go\n" +
			"M  staged.go\n")},
		keyRevList: {out: []byte("1\t2\n")},
		keyLog:     {out: logFixture("abc1234", "feat: do the thing", "Jonas Ross", "2026-05-17T12:34:56+00:00")},
	}
}

// logFixture builds a NUL-separated record matching the
// `git log -1 --format=%h%x00%s%x00%an%x00%cI` invocation.
func logFixture(hash, subject, author, ts string) []byte {
	return []byte(hash + "\x00" + subject + "\x00" + author + "\x00" + ts + "\n")
}

func TestWorktreeStatus_HappyPath(t *testing.T) {
	installFakeRunner(t, happyPathResponses())

	got, err := WorktreeStatus(context.Background(), "/repo/feat")
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}

	wantWhen, _ := time.Parse(time.RFC3339, "2026-05-17T12:34:56+00:00")
	want := Worktree{
		Path:        "/repo/feat",
		Branch:      "feat/things",
		DirtyFiles:  3,
		Ahead:       2,
		Behind:      1,
		HasUpstream: true,
		LastCommit: Commit{
			Hash:    "abc1234",
			Subject: "feat: do the thing",
			Author:  "Jonas Ross",
			When:    wantWhen.UTC(),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorktreeStatus mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestWorktreeStatus_NoUpstream(t *testing.T) {
	responses := happyPathResponses()
	responses[keyRevList] = fakeResponse{
		err: errors.New("exit status 128: fatal: no upstream configured for branch 'feat/things'"),
	}
	installFakeRunner(t, responses)

	got, err := WorktreeStatus(context.Background(), "/repo/feat")
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if got.HasUpstream {
		t.Errorf("HasUpstream=true, want false")
	}
	if got.Ahead != 0 || got.Behind != 0 {
		t.Errorf("Ahead=%d Behind=%d, want both zero", got.Ahead, got.Behind)
	}
	if got.Branch != "feat/things" {
		t.Errorf("Branch=%q, want feat/things", got.Branch)
	}
}

func TestWorktreeStatus_DirtyFileCount(t *testing.T) {
	cases := []struct {
		name  string
		status string
		want  int
	}{
		{
			name: "mixed modified untracked staged",
			status: " M a.go\n" +
				"M  b.go\n" +
				"?? c.go\n" +
				"A  d.go\n" +
				"R  e.go -> f.go\n",
			want: 5,
		},
		{
			name:   "single line no trailing newline",
			status: " M lonely.go",
			want:   1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := happyPathResponses()
			responses[keyStatus] = fakeResponse{out: []byte(tc.status)}
			installFakeRunner(t, responses)
			got, err := WorktreeStatus(context.Background(), "/repo")
			if err != nil {
				t.Fatalf("WorktreeStatus: %v", err)
			}
			if got.DirtyFiles != tc.want {
				t.Errorf("DirtyFiles=%d, want %d", got.DirtyFiles, tc.want)
			}
		})
	}
}

func TestWorktreeStatus_CleanRepo(t *testing.T) {
	responses := happyPathResponses()
	responses[keyStatus] = fakeResponse{out: []byte("")}
	installFakeRunner(t, responses)

	got, err := WorktreeStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if got.DirtyFiles != 0 {
		t.Errorf("DirtyFiles=%d, want 0", got.DirtyFiles)
	}
}

func TestWorktreeStatus_CommandFailure(t *testing.T) {
	// First invocation (symbolic-ref) fails with a non-detached-head
	// error. Must wrap and return; must not panic.
	responses := happyPathResponses()
	responses[keySymbolicRef] = fakeResponse{
		err: errors.New("exit status 128: fatal: not a git repository"),
	}
	installFakeRunner(t, responses)

	_, err := WorktreeStatus(context.Background(), "/nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "git: head:") {
		t.Errorf("error not wrapped as expected: %v", err)
	}
}

func TestWorktreeStatus_DetachedHead(t *testing.T) {
	// symbolic-ref reports detached HEAD as a non-zero exit with the
	// "not a symbolic ref" phrase. Treated as state, not error.
	responses := happyPathResponses()
	responses[keySymbolicRef] = fakeResponse{
		err: errors.New("exit status 1: fatal: ref HEAD is not a symbolic ref"),
	}
	installFakeRunner(t, responses)

	got, err := WorktreeStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if got.Branch != "" {
		t.Errorf("Branch=%q, want empty", got.Branch)
	}
	if !got.Detached {
		t.Errorf("Detached=false, want true")
	}
}

func TestWorktreeStatus_ParsesISOTimestamp(t *testing.T) {
	// %cI emits strict ISO 8601 with timezone offset. Verify a few
	// distinct forms parse to the expected UTC time.Time.
	cases := []struct {
		name string
		ts   string
		want time.Time
	}{
		{
			name: "utc Z",
			ts:   "2026-05-17T12:34:56+00:00",
			want: time.Date(2026, 5, 17, 12, 34, 56, 0, time.UTC),
		},
		{
			name: "positive offset",
			ts:   "2026-05-17T14:34:56+02:00",
			want: time.Date(2026, 5, 17, 12, 34, 56, 0, time.UTC),
		},
		{
			name: "negative offset",
			ts:   "2026-05-17T05:34:56-07:00",
			want: time.Date(2026, 5, 17, 12, 34, 56, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := happyPathResponses()
			responses[keyLog] = fakeResponse{
				out: logFixture("deadbee", "subject", "auth", tc.ts),
			}
			installFakeRunner(t, responses)
			got, err := WorktreeStatus(context.Background(), "/repo")
			if err != nil {
				t.Fatalf("WorktreeStatus: %v", err)
			}
			if !got.LastCommit.When.Equal(tc.want) {
				t.Errorf("When=%v, want %v", got.LastCommit.When, tc.want)
			}
		})
	}
}

func TestWorktreeStatus_LogEmptyRepo(t *testing.T) {
	// `git log -1` on a repo with no commits exits non-zero in practice,
	// but if a future git version returns empty stdout we shouldn't
	// crash; the LastCommit stays zero-valued.
	responses := happyPathResponses()
	responses[keyLog] = fakeResponse{out: []byte("")}
	installFakeRunner(t, responses)

	got, err := WorktreeStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if got.LastCommit != (Commit{}) {
		t.Errorf("LastCommit=%+v, want zero value", got.LastCommit)
	}
}

func TestParseWorktreeList_TrailingCRLFTolerant(t *testing.T) {
	// Git on Windows can emit CRLF line endings. The parser strips \r
	// so paths and refs don't pick up the carriage return.
	fixture := "worktree /repo/main\r\nbranch refs/heads/main\r\n\r\n"
	got := parseWorktreeList([]byte(fixture))
	want := []Worktree{{Path: "/repo/main", Branch: "main"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
