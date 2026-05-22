package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/pr"
)

const (
	branchColMin = 24
	branchColMax = 50
	statusColW   = 12
	prColW       = 14
	procsColW    = 3
	ageColW      = 6

	// Column visibility thresholds (width in cells).
	prVisibleWidth    = 100
	procsVisibleWidth = 120
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

// prStateGlyph returns the colored state glyph for a PR.
func prStateGlyph(p pr.PR) string {
	switch {
	case p.State == pr.PRStateMerged:
		return prStateMergedStyle.Render("⌧")
	case p.State == pr.PRStateClosed:
		return prStateClosedStyle.Render("✗")
	case p.IsDraft:
		return prStateDraftStyle.Render("◐")
	default:
		return prStateOpenStyle.Render("○")
	}
}

func prCIGlyph(rollup pr.CIStatus) string {
	switch rollup {
	case pr.CISuccess:
		return prCISuccessStyle.Render("✓")
	case pr.CIFailure:
		return prCIFailureStyle.Render("✗")
	case pr.CIPending:
		return prCIPendingStyle.Render("⋯")
	default:
		return " "
	}
}

func prReviewGlyph(state pr.ReviewState) string {
	switch state {
	case pr.ReviewApproved:
		return prReviewApprStyle.Render("A")
	case pr.ReviewChangesRequested:
		return prReviewChangeStyle.Render("C")
	case pr.ReviewRequired:
		return prReviewReqStyle.Render("R")
	default:
		return " "
	}
}

func renderPRCol(p *pr.PR, stale bool) string {
	if p == nil {
		return ""
	}
	num := prNumberStyle.Render(fmt.Sprintf("#%d", p.Number))
	state := prStateGlyph(*p)
	ci := prCIGlyph(p.CIRollup)
	rev := prReviewGlyph(p.ReviewState)
	out := num + " " + state + " " + ci + " " + rev
	if stale {
		out = prStaleStyle.Render("~") + out
	}
	return out
}

func renderProcs(state aggregator.WorktreeState) string {
	n := len(state.Procs)
	if n == 0 {
		return ""
	}
	for _, p := range state.Procs {
		if isClaudeProc(p.Command, p.Args) {
			return procsClaudeStyle.Render(fmt.Sprintf("%d*", n))
		}
	}
	return procsStyle.Render(fmt.Sprintf("%d", n))
}

func isClaudeProc(cmd string, args []string) bool {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "claude") {
		return true
	}
	for _, a := range args {
		if strings.Contains(strings.ToLower(a), "claude") {
			return true
		}
	}
	return false
}

func isMergedPR(state aggregator.WorktreeState) bool {
	return state.PR != nil && state.PR.State == pr.PRStateMerged
}

type rowOpts struct {
	branchColW int
	width      int
	// blinkOn picks the live-indicator phase: true → bright bold ●,
	// false → dim ●. Same for every Live row in a paint — all live
	// worktrees blink in unison.
	blinkOn bool
}

func renderRow(state aggregator.WorktreeState, branch string, focused bool, now time.Time, opts rowOpts) string {
	wt := state.Worktree
	merged := isMergedPR(state)

	cursor := "   "
	if focused {
		cursor = focusCursor.Render(" ▍ ")
	}

	// Primary worktree gets a leading ⌂ glyph; non-primary rows pad the
	// same 2-cell slot with spaces so every row aligns on the branch col.
	marker := "  "
	if wt.Main {
		marker = primaryMarkerStyle.Render("⌂ ")
	}

	branchText := truncate(branch, opts.branchColW)
	branchColStyle := lipgloss.NewStyle().Width(opts.branchColW)
	switch {
	case merged:
		branchColStyle = branchColStyle.Inherit(mergedStyle)
	case wt.Detached:
		branchColStyle = branchColStyle.Inherit(detachedStyle)
	case focused:
		branchColStyle = branchColStyle.Inherit(focusedBranch)
	default:
		branchColStyle = branchColStyle.Inherit(branchStyle)
	}
	branchCol := branchColStyle.Render(branchText)

	statusCol := statusColStyle.Render(renderStatus(wt.DirtyFiles, wt.Ahead, wt.Behind, wt.HasUpstream))

	parts := make([]string, 0, 16)
	parts = append(parts, cursor, marker, branchCol, "  ", statusCol)

	if opts.width >= prVisibleWidth {
		parts = append(parts, "  ", prColStyle.Render(renderPRCol(state.PR, state.PRStale)))
	}

	if opts.width >= procsVisibleWidth {
		parts = append(parts, "  ", procsColStyle.Render(renderProcs(state)))
	}

	parts = append(parts, "  ", ageColStyle.Render(FormatRelativeTime(wt.LastCommit.When, now)))

	if state.Live != nil {
		liveGlyph := liveDimStyle.Render("●")
		if opts.blinkOn {
			liveGlyph = liveStyle.Render("●")
		}
		parts = append(parts, "  ", liveGlyph, " ", modelStyle.Render(state.Live.Model))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
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
// branch substring. focusIndex indexes the unfiltered ordered list. blinkOn
// is the current phase of the live-indicator blink — applied uniformly to
// every Live row so all live indicators blink in unison.
func renderWorktreeList(
	ordered []string,
	states map[string]aggregator.WorktreeState,
	focusIndex int,
	filterStr string,
	now time.Time,
	width int,
	blinkOn bool,
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
		opts := rowOpts{
			branchColW: branchColW,
			width:      width,
			blinkOn:    blinkOn,
		}
		lines = append(lines, renderRow(state, branch, rawIdx == focusIndex, now, opts))
	}
	if len(lines) == 0 {
		return dimStyle.Render("   (no worktrees)")
	}
	return strings.Join(lines, "\n")
}
