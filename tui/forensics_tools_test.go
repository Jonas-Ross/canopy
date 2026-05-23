package tui_test

import (
	"strings"
	"testing"

	"github.com/jonasross/canopy/analytics"
	"github.com/jonasross/canopy/tui"
)

// TestForensicsToolsView_golden pins the tools sub-view golden frame.
func TestForensicsToolsView_golden(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewTools, scenarioAnalytics())
	assertGolden(t, "forensics_tools", frame(m))
}

// TestForensicsToolsView_modelHeadersPresent verifies both model section
// headers appear in the tools view.
func TestForensicsToolsView_modelHeadersPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewTools, scenarioAnalytics())
	view := stripANSI(m.View())
	for _, model := range []string{"claude-opus-4-7", "claude-sonnet-4-6"} {
		if !strings.Contains(view, model) {
			t.Errorf("tools view missing model header %q; view=\n%s", model, view)
		}
	}
}

// TestForensicsToolsView_otherRowPresent verifies the "other" collapse row
// appears (opus has 6 tools, only top-5 show individually).
func TestForensicsToolsView_otherRowPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewTools, scenarioAnalytics())
	view := stripANSI(m.View())
	if !strings.Contains(view, "other") {
		t.Errorf("tools view missing 'other' collapse row; view=\n%s", view)
	}
}

// TestForensicsToolsView_topToolsPresent verifies named tools appear.
func TestForensicsToolsView_topToolsPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewTools, scenarioAnalytics())
	view := stripANSI(m.View())
	for _, tool := range []string{"Bash", "Read", "Edit"} {
		if !strings.Contains(view, tool) {
			t.Errorf("tools view missing tool %q; view=\n%s", tool, view)
		}
	}
}

// TestForensicsToolsView_otherCountExceedsMaxNoPanic regression-guards a
// real workload where the long-tail "other" rollup count is larger than
// the single top-5 max (the bar normalization basis) — without the cells
// cap in toolBar this triggers strings.Repeat with a negative count and
// panics. See forensics_tools.go:toolBar.
func TestForensicsToolsView_otherCountExceedsMaxNoPanic(t *testing.T) {
	snap := scenarioAnalytics()
	// 8 tools each count=10 for one model: top-5 maxCount=10,
	// otherRows = 3 tools summing to 30. Unclamped formula:
	// (30 * 20) / 10 = 60 cells, pad = 20 - 60 = -40 → panic.
	snap.Tools = nil
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		snap.Tools = append(snap.Tools, analytics.ToolUsage{
			Model: "claude-opus-4-7", Tool: name, Count: 10,
		})
	}
	m := buildAnalyticsModel(t, tui.ViewTools, snap)
	// Calling View() exercises renderToolsView → toolBar(otherCount, maxCount).
	// Without the cap this panics; with the cap it returns a clean string.
	view := m.View()
	if view == "" {
		t.Fatalf("expected non-empty view")
	}
}
