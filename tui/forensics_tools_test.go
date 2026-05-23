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

// TestForensicsToolsView_headerSessionCountFromWindowNotSnapshot regression-
// guards the case where Snapshot.Sessions is capped (recentSessionsLimit=20)
// but the analytics window has more sessions for a model: the tools header
// must show the full-window count from SessionCountByModel, not 0 or a
// truncated count derived from the capped Sessions slice.
func TestForensicsToolsView_headerSessionCountFromWindowNotSnapshot(t *testing.T) {
	snap := scenarioAnalytics()
	snap.Sessions = nil // simulate a model whose sessions were all evicted by the cap
	snap.SessionCountByModel = map[string]int{
		"claude-opus-4-7":   42, // honest window count
		"claude-sonnet-4-6": 18,
	}
	m := buildAnalyticsModel(t, tui.ViewTools, snap)
	view := stripANSI(m.View())
	if !strings.Contains(view, "42 sessions") {
		t.Errorf("tools header should show 42 sessions for opus (full-window count); view=\n%s", view)
	}
	if !strings.Contains(view, "18 sessions") {
		t.Errorf("tools header should show 18 sessions for sonnet; view=\n%s", view)
	}
}

// TestForensicsToolsView_extremeValuesDoNotPanic sanity-checks that the
// renderer handles many same-count tools (forcing a big "other" rollup)
// and a single dominant tool, without panicking or producing empty
// output. The previous version of this test guarded a negative-padding
// bug in the old per-model-normalization formula; that formula is gone,
// so the test now serves as a general regression net.
func TestForensicsToolsView_extremeValuesDoNotPanic(t *testing.T) {
	snap := scenarioAnalytics()
	snap.Tools = nil
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		snap.Tools = append(snap.Tools, analytics.ToolUsage{
			Model: "claude-opus-4-7", Tool: name, Count: 10,
		})
	}
	m := buildAnalyticsModel(t, tui.ViewTools, snap)
	view := m.View()
	if view == "" {
		t.Fatalf("expected non-empty view")
	}
}
