package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jonasross/canopy/tui"
)

// TestFormatSessionTime exercises the formatSessionTime helper.
func TestFormatSessionTime(t *testing.T) {
	now := goldenClock // 2026-05-18 12:00:00 UTC

	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "-"},
		{"same day", now.Add(-30 * time.Minute), "11:30"},
		{"same day midnight edge", time.Date(2026, 5, 18, 0, 5, 0, 0, time.UTC), "00:05"},
		{"yesterday", now.Add(-25 * time.Hour), "Sun 17"},
		{"2 days ago", now.Add(-50 * time.Hour), "Sat 16"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.FormatSessionTime(tc.t, now); got != tc.want {
				t.Errorf("FormatSessionTime(%v, now) = %q, want %q", tc.t, got, tc.want)
			}
		})
	}
}

// TestForensicsSessionsView_golden pins the sessions sub-view golden frame.
func TestForensicsSessionsView_golden(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewSessions, scenarioAnalytics())
	assertGolden(t, "forensics_sessions", frame(m))
}

// TestForensicsSessionsView_liveDotsPresent verifies live-dot (●) is rendered
// for sessions within the live window.
func TestForensicsSessionsView_liveDotsPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewSessions, scenarioAnalytics())
	// Raw view preserves ANSI; strip and look for ●.
	view := stripANSI(m.View())
	count := strings.Count(view, "●")
	if count < 2 {
		t.Errorf("expected ≥2 live-dots (●) in sessions view, got %d; view=\n%s", count, view)
	}
}

// TestForensicsSessionsView_headerPresent verifies the column headers render.
func TestForensicsSessionsView_headerPresent(t *testing.T) {
	m := buildAnalyticsModel(t, tui.ViewSessions, scenarioAnalytics())
	view := stripANSI(m.View())
	for _, hdr := range []string{"started", "model", "worktree", "duration", "prompts", "tools"} {
		if !strings.Contains(view, hdr) {
			t.Errorf("sessions view missing header %q; view=\n%s", hdr, view)
		}
	}
}
