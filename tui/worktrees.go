package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/jonasross/canopy/aggregator"
)

// FormatRelativeTime returns a short human-readable duration for when,
// relative to now. Returns "now" for times within the last minute, then
// "Xm", "Xh", "Xd". A zero when returns "-".
func FormatRelativeTime(when, now time.Time) string {
	if when.IsZero() {
		return "-"
	}
	d := now.Sub(when)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FormatAheadBehind returns the ahead/behind indicator string.
// Returns "" when hasUpstream is false.
func FormatAheadBehind(ahead, behind int, hasUpstream bool) string {
	if !hasUpstream {
		return ""
	}
	if ahead == 0 && behind == 0 {
		return "="
	}
	var parts []string
	if ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", ahead))
	}
	if behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", behind))
	}
	return strings.Join(parts, " ")
}

// FormatBranch returns the display string for a branch: the branch name,
// or "(detached)" when detached is true.
func FormatBranch(branch string, detached bool) string {
	if detached {
		return "(detached)"
	}
	return branch
}

// renderRow renders one worktree row as a plain string. The focused flag
// applies the reverse-video style. now is injected so pure tests can fix
// the clock.
func renderRow(state aggregator.WorktreeState, focused bool, now time.Time) string {
	wt := state.Worktree
	var sb strings.Builder

	// Branch / detached.
	branch := FormatBranch(wt.Branch, wt.Detached)
	sb.WriteString(branch)

	// Dirty count.
	if wt.DirtyFiles > 0 {
		sb.WriteString(fmt.Sprintf("  ~%d", wt.DirtyFiles))
	}

	// Ahead/behind.
	if ab := FormatAheadBehind(wt.Ahead, wt.Behind, wt.HasUpstream); ab != "" {
		sb.WriteString("  ")
		sb.WriteString(ab)
	}

	// Last-commit age.
	sb.WriteString("  ")
	sb.WriteString(FormatRelativeTime(wt.LastCommit.When, now))

	// Live indicator.
	if state.Live != nil {
		sb.WriteString("  ")
		sb.WriteString(liveStyle.Render("●"))
		sb.WriteString(" ")
		sb.WriteString(state.Live.Model)
	}

	row := sb.String()
	if focused {
		return focusedStyle.Render(row)
	}
	return row
}

// renderWorktreeList renders all visible worktrees to a multi-line string.
// It filters by filterStr (case-insensitive branch-name substring) when non-empty.
// focusIndex is the index within the *unfiltered* ordered list.
func renderWorktreeList(
	ordered []string,
	states map[string]aggregator.WorktreeState,
	focusIndex int,
	filterStr string,
	now time.Time,
) string {
	var lines []string
	for rawIdx, path := range ordered {
		state, ok := states[path]
		if !ok {
			continue
		}
		wt := state.Worktree
		branch := FormatBranch(wt.Branch, wt.Detached)
		if filterStr != "" && !strings.Contains(strings.ToLower(branch), strings.ToLower(filterStr)) {
			continue
		}
		focused := rawIdx == focusIndex
		lines = append(lines, renderRow(state, focused, now))
	}
	if len(lines) == 0 {
		return "(no worktrees)"
	}
	return strings.Join(lines, "\n")
}

