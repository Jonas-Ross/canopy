package tui_test

import (
	"strings"
	"testing"

	"github.com/jonasross/canopy/tui"
)

// TestFormatTokens exercises the formatTokens helper via the exported seam.
func TestFormatTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{12400, "12.4K"},
		{999999, "1000.0K"},
		{1_000_000, "1.0M"},
		{12_400_000, "12.4M"},
		{1_234_567, "1.2M"},
	}
	for _, tc := range cases {
		if got := tui.FormatTokens(tc.in); got != tc.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestForensicsSpendView_golden pins the spend view golden frame.
func TestForensicsSpendView_golden(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewSpend, scenarioAnalytics())
	assertGolden(t, "forensics_spend", frame(m))
}

// TestForensicsSpendView_headerPresent verifies the spend view header is rendered.
func TestForensicsSpendView_headerPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewSpend, scenarioAnalytics())
	view := stripANSI(m.View())
	if !strings.Contains(view, "spend") {
		t.Errorf("spend view missing 'spend' header; view=\n%s", view)
	}
	if !strings.Contains(view, "last 30 days") {
		t.Errorf("spend view missing 'last 30 days' header; view=\n%s", view)
	}
}

// TestForensicsSpendView_tokenFormatsPresent verifies K/M formatted numbers appear.
func TestForensicsSpendView_tokenFormatsPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewSpend, scenarioAnalytics())
	view := stripANSI(m.View())
	// The scenario has multi-million token counts — should see M suffix.
	if !strings.Contains(view, "M") {
		t.Errorf("spend view missing 'M' token suffix; view=\n%s", view)
	}
}

// TestForensicsSpendView_sparklinePresent verifies unicode block chars appear.
func TestForensicsSpendView_sparklinePresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewSpend, scenarioAnalytics())
	view := m.View() // raw — block chars are not ANSI
	// At least one sparkline bar character should be present.
	hasBar := strings.ContainsAny(view, "▁▂▃▄▅▆▇█")
	if !hasBar {
		t.Errorf("spend view missing sparkline bar characters; view=\n%s", stripANSI(view))
	}
}
