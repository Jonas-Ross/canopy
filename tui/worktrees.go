package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/jonasross/canopy/aggregator"
)

// FormatRelativeTime returns "now" / "Xm" / "Xh" / "Xd"; zero when returns "-".
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

// FormatAheadBehind returns "↑N ↓M" / "=" / "" depending on upstream state.
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

// FormatBranch returns the branch name, or "(detached)" when detached.
func FormatBranch(branch string, detached bool) string {
	if detached {
		return "(detached)"
	}
	return branch
}

func renderRow(state aggregator.WorktreeState, branch string, focused bool, now time.Time) string {
	wt := state.Worktree
	var sb strings.Builder

	sb.WriteString(branch)

	if wt.DirtyFiles > 0 {
		sb.WriteString(fmt.Sprintf("  ~%d", wt.DirtyFiles))
	}

	if ab := FormatAheadBehind(wt.Ahead, wt.Behind, wt.HasUpstream); ab != "" {
		sb.WriteString("  ")
		sb.WriteString(ab)
	}

	sb.WriteString("  ")
	sb.WriteString(FormatRelativeTime(wt.LastCommit.When, now))

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

// renderWorktreeList renders visible worktrees, filtered by case-insensitive
// branch substring. focusIndex indexes the unfiltered ordered list.
func renderWorktreeList(
	ordered []string,
	states map[string]aggregator.WorktreeState,
	focusIndex int,
	filterStr string,
	now time.Time,
) string {
	lowerFilter := strings.ToLower(filterStr)
	lines := make([]string, 0, len(ordered))
	for rawIdx, path := range ordered {
		state, ok := states[path]
		if !ok {
			continue
		}
		branch := FormatBranch(state.Worktree.Branch, state.Worktree.Detached)
		if lowerFilter != "" && !strings.Contains(strings.ToLower(branch), lowerFilter) {
			continue
		}
		lines = append(lines, renderRow(state, branch, rawIdx == focusIndex, now))
	}
	if len(lines) == 0 {
		return "(no worktrees)"
	}
	return strings.Join(lines, "\n")
}
