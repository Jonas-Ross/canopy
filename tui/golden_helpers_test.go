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
