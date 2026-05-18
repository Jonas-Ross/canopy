package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/internal/demo"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/tui"
)

func TestGolden_BaseLayoutW80(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 80, 30)
	assertGolden(t, "base_w80", frame(m))
}

func TestGolden_PRColumnW100(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 100, 30)
	assertGolden(t, "pr_w100", frame(m))
}

func TestGolden_ProcsColumnW120(t *testing.T) {
	fixtures := scenarioFixtures()
	// Inject a claude proc on the feat/auth worktree so the procs column
	// renders the "claude here" marker (`*`).
	fixtures[1].Procs = []procs.Process{
		{Pid: 1234, Cwd: fixtures[1].Path, Command: "claude", Args: []string{"--print"}},
	}
	m := buildModel(t, fixtures, 120, 30)
	assertGolden(t, "procs_w120", frame(m))
}

func TestGolden_DetailPaneW140(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 140, 30)
	// Focus the second worktree (feat/auth) so the pane has rich content.
	m, _ = m.Update(sendKey('j'))
	assertGolden(t, "detail_w140", frame(m))
}

func TestGolden_FilterActive(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 100, 30)
	m, _ = m.Update(sendKey('/'))
	for _, r := range "feat" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(sendSpecialKey(tea.KeyEnter))
	assertGolden(t, "filter_active", frame(m))
}

func TestGolden_ConfirmPrune(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 100, 30)
	m, _ = m.Update(sendKey('j')) // focus feat/auth, not main
	m, _ = m.Update(sendKey('d'))
	assertGolden(t, "confirm_prune", frame(m))
}

func TestGolden_NewWorktreeForm(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 100, 30)
	m, _ = m.Update(sendKey('n'))
	for _, r := range "feat/new-thing" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	assertGolden(t, "new_worktree_form", frame(m))
}

func TestGolden_NoticeNoPR(t *testing.T) {
	fixtures := scenarioFixtures()
	// Focus is at index 0 (main) which has no PR. Press `p`.
	m := buildModel(t, fixtures, 100, 30)
	m, _ = m.Update(sendKey('p'))
	assertGolden(t, "notice_no_pr", frame(m))
}

func TestGolden_PulseActive(t *testing.T) {
	fixtures := scenarioFixtures()
	authIdx := 1
	withoutLive := fixtures[authIdx]
	withoutLive.Live = nil

	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, frozenNow())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// Seed the worktree without Live first so the next Update flips Live
	// from nil → non-nil and the pulse fires.
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: withoutLive.Path, State: toState(withoutLive)}))
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: fixtures[authIdx].Path, State: toState(fixtures[authIdx])}))

	assertGolden(t, "pulse_active", frame(m))

	// Color-regression guard: the previous incarnation of the pulse style
	// rendered as a solid block because Foreground and Background shared the
	// same color. Background SGR codes for green (42 / 102) must not appear.
	raw := rawFrame(m)
	if strings.Contains(raw, "\x1b[42m") || strings.Contains(raw, "\x1b[102m") {
		t.Errorf("pulse raw frame contains background-green SGR (regression: pulse-as-block):\n%q", raw)
	}
}

func TestGolden_PulseExpired(t *testing.T) {
	fixtures := scenarioFixtures()
	authIdx := 1
	withoutLive := fixtures[authIdx]
	withoutLive.Live = nil

	clk := goldenClock
	m := tui.NewModel(&fakeRefresher{})
	m = tui.SetNow(m, func() time.Time { return clk })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: withoutLive.Path, State: toState(withoutLive)}))
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: fixtures[authIdx].Path, State: toState(fixtures[authIdx])}))

	// Advance time past pulseDuration; View should drop the pulse style.
	clk = goldenClock.Add(2 * time.Second)
	assertGolden(t, "pulse_expired", frame(m))
}

// TestPulse_RawFramesDifferAcrossActiveExpired pins the invariant the two
// pulse goldens above only check loosely: after ANSI strip the layouts must
// match (same column widths, glyphs, branch names), but in raw form the
// active and expired frames must differ — otherwise the pulse color isn't
// actually being applied, even though the regression-block check passes.
// Catches a regression where livePulseStyle drifts back toward liveStyle.
func TestPulse_RawFramesDifferAcrossActiveExpired(t *testing.T) {
	fixtures := scenarioFixtures()
	authIdx := 1
	withoutLive := fixtures[authIdx]
	withoutLive.Live = nil

	build := func(advance time.Duration) string {
		clk := goldenClock
		m := tui.NewModel(&fakeRefresher{})
		m = tui.SetNow(m, func() time.Time { return clk })
		m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: withoutLive.Path, State: toState(withoutLive)}))
		m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: fixtures[authIdx].Path, State: toState(fixtures[authIdx])}))
		clk = goldenClock.Add(advance)
		return rawFrame(m)
	}

	active := build(0)
	expired := build(2 * time.Second)

	if active == expired {
		t.Fatalf("raw frames are byte-identical across active/expired pulse — color is not being applied. Active and expired must differ in raw form even though stripped layouts match.")
	}

	// Belt-and-braces: the active frame must use a yellow SGR (33 or 93)
	// somewhere; the expired one falls back to liveStyle (green). dirtyStyle
	// also uses yellow, but the fixture here has DirtyFiles=0, so any yellow
	// SGR in the active frame can only come from the pulse style.
	hasYellow := strings.Contains(active, "\x1b[33m") || strings.Contains(active, "\x1b[93m") ||
		strings.Contains(active, ";33m") || strings.Contains(active, ";93m")
	if !hasYellow {
		t.Errorf("active pulse frame contains no yellow SGR (33/93) — livePulseStyle may have drifted; raw=%q", active)
	}
}

func TestGolden_ProcsPanelCollapsed(t *testing.T) {
	fixtures := scenarioFixtures()
	fixtures[1].Procs = demo.HeavyProcs(fixtures[1].Path)
	m := buildModel(t, fixtures, 140, 40)
	m, _ = m.Update(sendKey('j'))
	assertGolden(t, "procs_panel_collapsed", frame(m))
}

func TestGolden_ProcsPanelExpanded(t *testing.T) {
	fixtures := scenarioFixtures()
	fixtures[1].Procs = demo.HeavyProcs(fixtures[1].Path)
	m := buildModel(t, fixtures, 140, 40)
	m, _ = m.Update(sendKey('j'))
	m, _ = m.Update(sendKey('P'))
	assertGolden(t, "procs_panel_expanded", frame(m))
}

func TestGolden_DetachedHead(t *testing.T) {
	fixtures := []fixtureWorktree{
		{
			Path:       "/tmp/canopy-demo/repo/.worktrees/legacy",
			Detached:   true,
			CommitWhen: goldenClock.Add(-5 * time.Minute),
			Subject:    "drift",
		},
	}
	m := buildModel(t, fixtures, 100, 30)
	assertGolden(t, "detached", frame(m))
}
