package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/analytics"
)

// renderWorktreesView renders the worktrees sub-view: columns worktree ·
// sessions · total time · last seen, with a footer totals row. A leading
// green "●" marks worktrees with activity inside liveWindowDuration. The
// last-seen column tints by recency (green for live/recent, foreground
// for today, yellow for this week, dim for older).
func renderWorktreesView(worktrees []analytics.WorktreeSummary, now time.Time, width int) string {
	if len(worktrees) == 0 {
		return dimStyle.Render("  no worktree data")
	}

	const (
		markerColW   = 2  // "● " or "  "
		worktreeColW = 24 // basename, truncated
		sessionsColW = 8  // "sessions"
		timeColW     = 10 // "total time"
		lastSeenColW = 10 // "last seen"
	)

	var sb strings.Builder
	sb.Grow(512)

	sb.WriteString(strings.Repeat(" ", markerColW))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", worktreeColW, "worktree")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%*s", sessionsColW, "sessions")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%*s", timeColW, "total time")))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", lastSeenColW, "last seen")))
	sb.WriteByte('\n')

	var totSessions int
	var totTime time.Duration

	for _, wt := range worktrees {
		name := truncateWithEllipsis(filepath.Base(wt.Path), worktreeColW)
		dur := formatTotalTime(wt.TotalTime)
		last := FormatRelativeTime(wt.LastSeen, now)
		age := now.Sub(wt.LastSeen)

		if !wt.LastSeen.IsZero() && age < liveWindowDuration {
			sb.WriteString(liveStyle.Render("●"))
			sb.WriteByte(' ')
		} else {
			sb.WriteString(strings.Repeat(" ", markerColW))
		}
		sb.WriteString(repoStyle.Render(fmt.Sprintf("%-*s", worktreeColW, name)))
		sb.WriteString("  ")
		sb.WriteString(detailValueStyle.Render(fmt.Sprintf("%*d", sessionsColW, wt.SessionCount)))
		sb.WriteString("  ")
		sb.WriteString(detailValueStyle.Render(fmt.Sprintf("%*s", timeColW, dur)))
		sb.WriteString("  ")
		sb.WriteString(lastSeenStyle(age, wt.LastSeen).Render(fmt.Sprintf("%-*s", lastSeenColW, last)))
		sb.WriteByte('\n')

		totSessions += wt.SessionCount
		totTime += wt.TotalTime
	}

	ruleLen := markerColW + worktreeColW + sessionsColW + timeColW + lastSeenColW + 8
	sb.WriteString("  ")
	sb.WriteString(ruleStyle.Render(strings.Repeat("─", ruleLen)))
	sb.WriteByte('\n')

	sb.WriteString(strings.Repeat(" ", markerColW))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", worktreeColW, "total")))
	sb.WriteString("  ")
	sb.WriteString(detailValueStyle.Render(fmt.Sprintf("%*d", sessionsColW, totSessions)))
	sb.WriteString("  ")
	sb.WriteString(detailValueStyle.Render(fmt.Sprintf("%*s", timeColW, formatTotalTime(totTime))))

	return sb.String()
}

// lastSeenStyle picks a recency-tinted style for the last-seen column:
// green for live/recent, foreground for today, yellow for this week,
// dim for older or never-seen.
func lastSeenStyle(age time.Duration, lastSeen time.Time) lipgloss.Style {
	switch {
	case lastSeen.IsZero():
		return dimStyle
	case age < liveWindowDuration:
		return liveStyle
	case age < time.Hour:
		return liveDimStyle
	case age < 24*time.Hour:
		return detailValueStyle
	case age < 7*24*time.Hour:
		return dirtyStyle
	default:
		return dimStyle
	}
}

// formatTotalTime formats a duration as "Xh Ym" or "Xm".
func formatTotalTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
