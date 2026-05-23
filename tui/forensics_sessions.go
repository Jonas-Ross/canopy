package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/analytics"
)

// liveWindowDuration is how recently a session must have updated to show the
// live-dot prefix (●). Mirrors aggregator.LiveWindow.
const liveWindowDuration = aggregator.LiveWindow

// formatSessionTime formats a session start time for display in the sessions
// table. If t is on the same UTC day as now, shows "HH:MM". Otherwise shows
// "Mon DD" (e.g. "Mon 22").
func formatSessionTime(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	yy, mm, dd := t.UTC().Date()
	ny, nm, nd := now.UTC().Date()
	if yy == ny && mm == nm && dd == nd {
		return fmt.Sprintf("%02d:%02d", t.UTC().Hour(), t.UTC().Minute())
	}
	return fmt.Sprintf("%s %02d", t.UTC().Format("Mon"), t.UTC().Day())
}

// shortModel converts a full model name to its short display form.
// "claude-opus-4-7" → "opus-4-7", "claude-sonnet-4-6" → "sonnet-4-6".
// Unknown prefixes pass through unchanged.
func shortModel(model string) string {
	const prefix = "claude-"
	if strings.HasPrefix(model, prefix) {
		return model[len(prefix):]
	}
	return model
}

// formatDuration formats a session duration in "47m" or "1h 02m" style.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %02dm", h, m)
}

// truncateWithEllipsis truncates s to at most max runes with a trailing "…".
func truncateWithEllipsis(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// worktreeLabel renders a Session.Worktree path into a human label.
// filepath.Base("") returns "." and filepath.Base("/") returns "/",
// both of which read as broken in the sessions table — render an
// explicit em-dash placeholder so the column is honest about missing data.
func worktreeLabel(path string, max int) string {
	if path == "" {
		return truncateWithEllipsis("—", max)
	}
	base := filepath.Base(path)
	if base == "." || base == "/" {
		return truncateWithEllipsis("—", max)
	}
	return truncateWithEllipsis(base, max)
}

// renderSessionsView renders the sessions sub-view: header, one row per
// session sorted DESC by UpdatedAt, with a live-dot (●) prefix for recently
// active sessions.
func renderSessionsView(sessions []analytics.SessionSummary, now time.Time, width int) string {
	if len(sessions) == 0 {
		return dimStyle.Render("  no session data")
	}

	_ = width // reserved for future column-hiding

	const (
		startedW  = 8  // "HH:MM" or "Mon DD"
		modelW    = 14 // "sonnet-4-6" fits; "opus-4-7" fits
		worktreeW = 18 // basename, truncated
		durationW = 10 // "1h 02m"
		promptsW  = 8  // "prompts" header
		toolsW    = 7  // "tools" header
	)

	var sb strings.Builder
	sb.Grow(512)

	// Header row.
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render("  ")) // live-dot column placeholder
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", startedW, "started")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", modelW, "model")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", worktreeW, "worktree")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", durationW, "duration")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%*s", promptsW, "prompts")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%*s", toolsW, "tools")))
	sb.WriteByte('\n')

	for _, s := range sessions {
		live := now.Sub(s.UpdatedAt) < liveWindowDuration

		var dot string
		if live {
			dot = liveStyle.Render("●")
		} else {
			dot = dimStyle.Render(" ")
		}

		started := formatSessionTime(s.StartedAt, now)
		model := shortModel(s.Model)
		wt := worktreeLabel(s.Worktree, worktreeW)
		dur := formatDuration(s.Duration)

		sb.WriteString("  ")
		sb.WriteString(dot)
		sb.WriteString(" ")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", startedW, started)))
		sb.WriteString("  ")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", modelW, model)))
		sb.WriteString("  ")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", worktreeW, wt)))
		sb.WriteString("  ")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", durationW, dur)))
		sb.WriteString("  ")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("%*d", promptsW, s.Prompts)))
		sb.WriteString("  ")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("%*d", toolsW, s.ToolCalls)))
		sb.WriteByte('\n')
	}

	return strings.TrimRight(sb.String(), "\n")
}
