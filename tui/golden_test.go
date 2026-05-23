package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// TestGolden_BlinkOnPhase pins the on-phase (bright bold green ●) frame
// for a Live worktree. The on-phase is the default blinkPhase after a
// Live transition; no tick-firing required.
func TestGolden_BlinkOnPhase(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 100, 30)
	m = tui.SetBlinkPhaseForTest(m, true)
	assertGolden(t, "blink_on", frame(m))

	// Color-regression guard: live-as-block (Background(colGreen) on a
	// green glyph rendering as a solid block) must not return. Background
	// SGR codes for green (42 / 102) must not appear.
	raw := rawFrame(m)
	if strings.Contains(raw, "\x1b[42m") || strings.Contains(raw, "\x1b[102m") {
		t.Errorf("on-phase raw frame contains background-green SGR (regression: live-as-block):\n%q", raw)
	}
}

// TestGolden_BlinkOffPhase pins the off-phase (dim non-bold green ●)
// frame for a Live worktree. Both phases keep the column layout — only
// the SGR changes — so the stripped goldens differ only via the SGR-aware
// tools downstream (raw-frame test below).
func TestGolden_BlinkOffPhase(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 100, 30)
	m = tui.SetBlinkPhaseForTest(m, false)
	assertGolden(t, "blink_off", frame(m))
}

// TestBlink_RawFramesDifferAcrossPhases pins the invariant the two blink
// goldens above only check loosely: after ANSI strip the layouts must
// match (same column widths, glyphs, branch names), but in raw form the
// on-phase and off-phase frames must differ — otherwise the blink style
// isn't actually being applied. Catches a regression where liveDimStyle
// drifts back toward liveStyle (or vice versa).
func TestBlink_RawFramesDifferAcrossPhases(t *testing.T) {
	build := func(phase bool) string {
		m := buildModel(t, scenarioFixtures(), 100, 30)
		m = tui.SetBlinkPhaseForTest(m, phase)
		return rawFrame(m)
	}

	on := build(true)
	off := build(false)

	if on == off {
		t.Fatalf("raw frames are byte-identical across on/off blink phases — style is not being applied. The two phases must differ in raw form even though stripped layouts match.")
	}

	// Belt-and-braces: the on-phase frame must use a bold SGR somewhere
	// (\x1b[1m or a combined SGR ending in ;1m / starting with 1;). The
	// fixture's live worktree has no other bold spans on the live-glyph
	// position, so a missing bold here means liveStyle has lost its bold
	// or the renderer isn't switching styles. Off-phase falls back to
	// liveDimStyle which has no bold attribute on the glyph.
	hasBold := strings.Contains(on, "\x1b[1m") || strings.Contains(on, ";1m") || strings.Contains(on, "\x1b[1;")
	if !hasBold {
		t.Errorf("on-phase frame contains no bold SGR — liveStyle may have lost its bold attribute; raw=%q", on)
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
