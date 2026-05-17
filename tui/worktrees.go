package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/aggregator"
)

const (
	branchColMin = 24
	branchColMax = 50
	statusColW   = 12
	ageColW      = 6
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

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// renderStatus renders the dirty + ahead/behind cluster with per-segment color.
func renderStatus(dirtyFiles, ahead, behind int, hasUpstream bool) string {
	var parts []string
	if dirtyFiles > 0 {
		parts = append(parts, dirtyStyle.Render(fmt.Sprintf("~%d", dirtyFiles)))
	}
	if hasUpstream {
		switch {
		case ahead == 0 && behind == 0:
			parts = append(parts, syncStyle.Render("="))
		default:
			var ab strings.Builder
			if ahead > 0 {
				ab.WriteString(aheadStyle.Render(fmt.Sprintf("↑%d", ahead)))
			}
			if behind > 0 {
				if ab.Len() > 0 {
					ab.WriteString(" ")
				}
				ab.WriteString(behindStyle.Render(fmt.Sprintf("↓%d", behind)))
			}
			parts = append(parts, ab.String())
		}
	}
	return strings.Join(parts, " ")
}

func renderRow(state aggregator.WorktreeState, branch string, focused bool, now time.Time, branchColW int) string {
	wt := state.Worktree

	cursor := "   "
	if focused {
		cursor = focusCursor.Render(" ▍ ")
	}

	branchText := truncate(branch, branchColW)
	var branchCol string
	switch {
	case wt.Detached:
		branchCol = lipgloss.NewStyle().Width(branchColW).Inherit(detachedStyle).Render(branchText)
	case focused:
		branchCol = lipgloss.NewStyle().Width(branchColW).Inherit(focusedBranch).Render(branchText)
	default:
		branchCol = lipgloss.NewStyle().Width(branchColW).Inherit(branchStyle).Render(branchText)
	}

	statusCol := lipgloss.NewStyle().Width(statusColW).Render(renderStatus(wt.DirtyFiles, wt.Ahead, wt.Behind, wt.HasUpstream))
	ageCol := lipgloss.NewStyle().Width(ageColW).Inherit(ageStyle).Render(FormatRelativeTime(wt.LastCommit.When, now))

	var liveCol string
	if state.Live != nil {
		liveCol = liveStyle.Render("●") + " " + modelStyle.Render(state.Live.Model)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, cursor, branchCol, "  ", statusCol, "  ", ageCol, "  ", liveCol)
}

func computeBranchColW(ordered []string, states map[string]aggregator.WorktreeState) int {
	w := branchColMin
	for _, path := range ordered {
		state, ok := states[path]
		if !ok {
			continue
		}
		n := utf8.RuneCountInString(FormatBranch(state.Worktree.Branch, state.Worktree.Detached))
		if n > w {
			w = n
		}
	}
	if w > branchColMax {
		w = branchColMax
	}
	return w
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
	branchColW := computeBranchColW(ordered, states)
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
		lines = append(lines, renderRow(state, branch, rawIdx == focusIndex, now, branchColW))
	}
	if len(lines) == 0 {
		return dimStyle.Render("   (no worktrees)")
	}
	return strings.Join(lines, "\n")
}
