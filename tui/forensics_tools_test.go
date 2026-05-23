package tui_test

import (
	"strings"
	"testing"

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
