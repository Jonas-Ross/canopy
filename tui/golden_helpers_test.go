package tui_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/analytics"
	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/internal/ansi"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/sessions"
	"github.com/jonasross/canopy/tui"
)

// updateGoldens regenerates golden files instead of comparing against them.
// Triggered with `go test ./tui -update`.
var updateGoldens = flag.Bool("update", false, "rewrite golden files in tui/testdata/golden/")

// stripANSI is a thin alias kept so existing tui_test code (ops_test.go,
// model_test.go) can call a familiar local helper. New code should call
// ansi.Strip directly.
func stripANSI(s string) string { return ansi.Strip(s) }

// goldenClock is the frozen reference time used by every fixture in this
// suite. Picked far enough from real wall-clock that relative-time strings
// produced by FormatRelativeTime never accidentally match "now".
var goldenClock = time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

func frozenNow() func() time.Time { return func() time.Time { return goldenClock } }

func init() {
	// Force lipgloss to emit ANSI escapes so render-time styling decisions
	// (foreground, italic, bold) show up in the captured frame. Goldens
	// strip them before compare; raw-frame assertions inspect them directly.
	//
	// Side effect: every test in tui_test now runs against an ANSI-emitting
	// renderer. Any new test that asserts on raw View() output must
	// stripANSI first (or use ansi.Strip), otherwise substring checks will
	// fail when styled spans get split by SGR escapes.
	lipgloss.SetColorProfile(termenv.ANSI)
}

// fixtureWorktree is the minimal input for a deterministic WorktreeState.
type fixtureWorktree struct {
	Path       string
	Branch     string
	Detached   bool
	Main       bool
	Dirty      int
	HasUp      bool
	Ahead      int
	Behind     int
	CommitWhen time.Time
	Subject    string
	PR         *pr.PR
	PRStale    bool
	Procs      []procs.Process
	Live       *sessions.Session
	Recent     []*sessions.Session
}

func toState(f fixtureWorktree) aggregator.WorktreeState {
	return aggregator.WorktreeState{
		Repo: aggregator.Repo{Name: "canopy-demo", Root: "/tmp/canopy-demo/repo"},
		Worktree: git.Worktree{
			Path:        f.Path,
			Branch:      f.Branch,
			Detached:    f.Detached,
			Main:        f.Main,
			DirtyFiles:  f.Dirty,
			HasUpstream: f.HasUp,
			Ahead:       f.Ahead,
			Behind:      f.Behind,
			LastCommit:  git.Commit{When: f.CommitWhen, Subject: f.Subject, Hash: "abc1234"},
		},
		PR:        f.PR,
		PRStale:   f.PRStale,
		Procs:     f.Procs,
		Live:      f.Live,
		Recent:    f.Recent,
		UpdatedAt: goldenClock,
	}
}

// scenarioFixtures returns the canonical 5-worktree set used by most goldens.
// Branch/PR/session shape mirrors what `canopy demo` produces.
func scenarioFixtures() []fixtureWorktree {
	authSess := &sessions.Session{
		ID:        "11111111-1111-1111-1111-111111111111",
		Model:     "claude-opus-4-7",
		Cwds:      []string{"/tmp/canopy-demo/repo/.worktrees/feat+auth"},
		StartedAt: goldenClock.Add(-10 * time.Minute),
		UpdatedAt: goldenClock.Add(-10 * time.Second),
	}
	depsSess := &sessions.Session{
		ID:        "22222222-2222-2222-2222-222222222222",
		Model:     "claude-sonnet-4-6",
		Cwds:      []string{"/tmp/canopy-demo/repo/.worktrees/chore+deps"},
		StartedAt: goldenClock.Add(-1 * time.Hour),
		UpdatedAt: goldenClock.Add(-5 * time.Minute),
	}
	return []fixtureWorktree{
		{
			Path: "/tmp/canopy-demo/repo", Branch: "main", Main: true,
			CommitWhen: goldenClock.Add(-2 * time.Hour),
			Subject:    "init",
		},
		{
			Path: "/tmp/canopy-demo/repo/.worktrees/feat+auth", Branch: "feat/auth",
			HasUp: true, Ahead: 1, Behind: 1,
			CommitWhen: goldenClock.Add(-35 * time.Minute),
			Subject:    "auth: bcrypt migration",
			Live:       authSess, Recent: []*sessions.Session{authSess},
			PR: &pr.PR{
				Number: 42, Title: "auth: bcrypt migration", HeadBranch: "feat/auth",
				State: pr.PRStateOpen, CIRollup: pr.CISuccess, ReviewState: pr.ReviewRequired,
				URL: "https://example.invalid/canopy-demo/pull/42",
			},
		},
		{
			Path: "/tmp/canopy-demo/repo/.worktrees/feat+dashboard", Branch: "feat/dashboard",
			HasUp: true, Behind: 1, Dirty: 3,
			CommitWhen: goldenClock.Add(-3 * time.Hour),
			Subject:    "dashboard: shipping screens",
			PR: &pr.PR{
				Number: 43, HeadBranch: "feat/dashboard",
				State: pr.PRStateOpen, IsDraft: true, CIRollup: pr.CIPending,
				URL: "https://example.invalid/canopy-demo/pull/43",
			},
		},
		{
			Path: "/tmp/canopy-demo/repo/.worktrees/fix+login", Branch: "fix/login",
			HasUp: true, Behind: 1,
			CommitWhen: goldenClock.Add(-25 * time.Hour),
			Subject:    "fix: login redirect",
			PR: &pr.PR{
				Number: 41, HeadBranch: "fix/login",
				State: pr.PRStateMerged, CIRollup: pr.CISuccess, ReviewState: pr.ReviewApproved,
			},
		},
		{
			Path: "/tmp/canopy-demo/repo/.worktrees/chore+deps", Branch: "chore/deps",
			HasUp: true, Ahead: 2, Behind: 1,
			CommitWhen: goldenClock.Add(-72 * time.Hour),
			Subject:    "chore: bump deps",
			Recent:     []*sessions.Session{depsSess},
			PR: &pr.PR{
				Number: 40, HeadBranch: "chore/deps",
				State: pr.PRStateClosed, CIRollup: pr.CIFailure, ReviewState: pr.ReviewChangesRequested,
			},
		},
	}
}

// buildModel constructs a Model seeded with fixtures, sized to (width, height),
// with a frozen clock.
func buildModel(t *testing.T, fixtures []fixtureWorktree, width, height int) tea.Model {
	t.Helper()
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, frozenNow())
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	for _, f := range fixtures {
		m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: f.Path, State: toState(f)}))
	}
	return m
}

// frame returns the ANSI-stripped View output.
func frame(m tea.Model) string { return stripANSI(m.View()) }

// rawFrame returns View() with ANSI sequences intact.
func rawFrame(m tea.Model) string { return m.View() }

func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name+".txt")
}

// assertGolden compares `got` to the named golden. With -update, rewrites it.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	// Trim trailing whitespace per line + a single trailing newline keeps
	// goldens stable across editors.
	got = normalizeGolden(got)
	p := goldenPath(name)
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	wantBytes, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden %s (run `go test ./tui -update` to create): %v", p, err)
	}
	if got != string(wantBytes) {
		t.Errorf("golden %s mismatch.\n--- want ---\n%s\n--- got ---\n%s", name, string(wantBytes), got)
	}
}

func normalizeGolden(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// scenarioAnalytics returns a deterministic, realistic analytics.Snapshot
// for golden-frame tests. All timestamps are relative to goldenClock
// (2026-05-18 12:00 UTC). The data is designed to exercise every renderer:
// sparkline peak, zero-activity days, live-dot sessions, tool "other"
// collapse, and per-worktree totals.
func scenarioAnalytics() analytics.Snapshot {
	now := goldenClock // 2026-05-18 12:00 UTC

	// Helper: UTC midnight N days before now.
	day := func(daysAgo int) time.Time {
		return now.Add(-time.Duration(daysAgo) * 24 * time.Hour).Truncate(24 * time.Hour)
	}

	// Days: 10 days with activity, descending. day-1 is the peak. Days 4 and 7
	// have zero activity (rendered as "·" placeholder). Snapshot.Days is sorted
	// DESC so index 0 = most recent.
	days := []analytics.DayBucket{
		{Date: day(1), Tokens: sessions.TokenStats{Input: 4_200_000, Output: 1_100_000, CacheRead: 8_100_000, CacheCreation: 1_200_000}, SessionCount: 6},
		{Date: day(2), Tokens: sessions.TokenStats{Input: 1_800_000, Output: 480_000, CacheRead: 3_200_000, CacheCreation: 400_000}, SessionCount: 3},
		{Date: day(3), Tokens: sessions.TokenStats{Input: 2_600_000, Output: 640_000, CacheRead: 5_100_000, CacheCreation: 700_000}, SessionCount: 4},
		// day 4 intentionally absent — zero-activity day
		{Date: day(5), Tokens: sessions.TokenStats{Input: 900_000, Output: 220_000, CacheRead: 1_500_000, CacheCreation: 200_000}, SessionCount: 2},
		{Date: day(6), Tokens: sessions.TokenStats{Input: 1_200_000, Output: 310_000, CacheRead: 2_400_000, CacheCreation: 300_000}, SessionCount: 2},
		// day 7 intentionally absent — zero-activity day
		{Date: day(8), Tokens: sessions.TokenStats{Input: 600_000, Output: 150_000, CacheRead: 900_000, CacheCreation: 100_000}, SessionCount: 1},
		{Date: day(9), Tokens: sessions.TokenStats{Input: 750_000, Output: 190_000, CacheRead: 1_100_000, CacheCreation: 150_000}, SessionCount: 1},
		{Date: day(10), Tokens: sessions.TokenStats{Input: 300_000, Output: 80_000, CacheRead: 400_000, CacheCreation: 50_000}, SessionCount: 1},
		{Date: day(15), Tokens: sessions.TokenStats{Input: 400_000, Output: 100_000, CacheRead: 600_000, CacheCreation: 80_000}, SessionCount: 1},
		{Date: day(22), Tokens: sessions.TokenStats{Input: 200_000, Output: 50_000, CacheRead: 300_000, CacheCreation: 40_000}, SessionCount: 1},
	}

	// Sessions: 8 rows sorted DESC by UpdatedAt.
	// Two within the 120s live window (updated < 2 min ago).
	sessions8 := []analytics.SessionSummary{
		// Live: updated 30s ago
		{
			ID: "aaaa-0001", Model: "claude-opus-4-7",
			Worktree:  "/repo/.worktrees/feat+auth",
			StartedAt: now.Add(-45 * time.Minute), UpdatedAt: now.Add(-30 * time.Second),
			Duration: 45 * time.Minute, Prompts: 12, ToolCalls: 47,
		},
		// Live: updated 90s ago
		{
			ID: "aaaa-0002", Model: "claude-sonnet-4-6",
			Worktree:  "/repo",
			StartedAt: now.Add(-20 * time.Minute), UpdatedAt: now.Add(-90 * time.Second),
			Duration: 20 * time.Minute, Prompts: 8, ToolCalls: 22,
		},
		// Recent today
		{
			ID: "aaaa-0003", Model: "claude-opus-4-7",
			Worktree:  "/repo/.worktrees/chore+deps",
			StartedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour),
			Duration: 1 * time.Hour, Prompts: 18, ToolCalls: 63,
		},
		{
			ID: "aaaa-0004", Model: "claude-sonnet-4-6",
			Worktree:  "/repo/.worktrees/feat+auth",
			StartedAt: now.Add(-5 * time.Hour), UpdatedAt: now.Add(-4 * time.Hour),
			Duration: 47 * time.Minute, Prompts: 9, ToolCalls: 31,
		},
		// Yesterday
		{
			ID: "aaaa-0005", Model: "claude-opus-4-7",
			Worktree:  "/repo",
			StartedAt: now.Add(-26 * time.Hour), UpdatedAt: now.Add(-25 * time.Hour),
			Duration: 1*time.Hour + 12*time.Minute, Prompts: 21, ToolCalls: 78,
		},
		{
			ID: "aaaa-0006", Model: "claude-sonnet-4-6",
			Worktree:  "/repo/.worktrees/chore+deps",
			StartedAt: now.Add(-28 * time.Hour), UpdatedAt: now.Add(-27 * time.Hour),
			Duration: 38 * time.Minute, Prompts: 7, ToolCalls: 19,
		},
		{
			ID: "aaaa-0007", Model: "claude-opus-4-7",
			Worktree:  "/repo/.worktrees/feat+auth",
			StartedAt: now.Add(-50 * time.Hour), UpdatedAt: now.Add(-49 * time.Hour),
			Duration: 55 * time.Minute, Prompts: 14, ToolCalls: 52,
		},
		{
			ID: "aaaa-0008", Model: "claude-sonnet-4-6",
			Worktree:  "/repo",
			StartedAt: now.Add(-75 * time.Hour), UpdatedAt: now.Add(-74 * time.Hour),
			Duration: 28 * time.Minute, Prompts: 5, ToolCalls: 14,
		},
	}

	// Tools: sorted (Model asc, Count desc). opus has 6 tools (tests "other"
	// collapse); sonnet has 3.
	tools := []analytics.ToolUsage{
		{Model: "claude-opus-4-7", Tool: "Bash", Count: 812},
		{Model: "claude-opus-4-7", Tool: "Read", Count: 487},
		{Model: "claude-opus-4-7", Tool: "Edit", Count: 295},
		{Model: "claude-opus-4-7", Tool: "Grep", Count: 153},
		{Model: "claude-opus-4-7", Tool: "Write", Count: 75},
		{Model: "claude-opus-4-7", Tool: "Glob", Count: 25},
		{Model: "claude-sonnet-4-6", Tool: "Bash", Count: 189},
		{Model: "claude-sonnet-4-6", Tool: "Read", Count: 97},
		{Model: "claude-sonnet-4-6", Tool: "Edit", Count: 86},
	}

	// Worktrees: 4 rows sorted DESC by TotalTime.
	worktrees := []analytics.WorktreeSummary{
		{Path: "/repo/.worktrees/feat+auth", SessionCount: 3, TotalTime: 2*time.Hour + 27*time.Minute, LastSeen: now.Add(-30 * time.Second)},
		{Path: "/repo", SessionCount: 3, TotalTime: 2*time.Hour + 0*time.Minute, LastSeen: now.Add(-90 * time.Second)},
		{Path: "/repo/.worktrees/chore+deps", SessionCount: 2, TotalTime: 1*time.Hour + 38*time.Minute, LastSeen: now.Add(-27 * time.Hour)},
		{Path: "/repo/.worktrees/fix+login", SessionCount: 0, TotalTime: 0, LastSeen: now.Add(-72 * time.Hour)},
	}

	return analytics.Snapshot{
		GeneratedAt: now,
		// WindowStart = UTC midnight 29 days before now's UTC day —
		// inclusive 30-day range when day-truncated by the renderer.
		WindowStart: time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -29),
		WindowEnd:   now,
		Days:        days,
		Sessions:    sessions8,
		Tools:       tools,
		Worktrees:   worktrees,
		// SessionCountByModel reflects the full window and is intentionally
		// out of sync with the visible per-model counts in sessions8 above
		// (4 opus / 4 sonnet) — this stands in for a real store where the
		// length-capped Sessions slice undercounts what the tools header
		// must report, and ensures the golden fails if the renderer
		// regresses to counting Snapshot.Sessions.
		SessionCountByModel: map[string]int{
			"claude-opus-4-7":   7,
			"claude-sonnet-4-6": 5,
		},
	}
}

// buildAnalyticsModel constructs a Model on the forensics tab with the given
// sub-view active and the snapshot injected. Sized to (width=140, height=40),
// frozen clock.
func buildAnalyticsModel(t *testing.T, view tui.View, snap analytics.Snapshot) tea.Model {
	t.Helper()
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, frozenNow())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	// Switch to forensics tab.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	// Inject analytics snapshot.
	m, _ = m.Update(tui.AnalyticsLoadedMsg{Snapshot: snap})
	// Navigate to the requested sub-view via digit keys.
	switch view {
	case tui.ViewSpend:
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	case tui.ViewSessions:
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	case tui.ViewTools:
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	case tui.ViewWorktrees:
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	}
	return m
}
