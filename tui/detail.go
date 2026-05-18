package tui

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/sessions"
)

const (
	detailPaneVisibleWidth = 140
	// detailPaneWidth is the outer pane width including left border + padding
	// (3 chars). Tuned so a 12-char label + 44-char value fits on one line.
	detailPaneWidth = 60
	// labelW is the fixed label-column width inside the pane.
	labelW = 12
	// detailBorderOverhead is BorderLeft(1) + PaddingLeft(2).
	detailBorderOverhead = 3
)

// detailContentW is the usable width inside the border, for sizing wraps.
const detailContentW = detailPaneWidth - detailBorderOverhead

// valueW is the column width for the right-hand value after a label.
const valueW = detailContentW - labelW

// elidePath shortens a filesystem path to fit max chars by replacing $HOME
// with "~" and tail-eliding with "…" if still too long.
func elidePath(path string, max int) string {
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(path, home) {
		path = "~" + path[len(home):]
	}
	if utf8.RuneCountInString(path) <= max {
		return path
	}
	runes := []rune(path)
	return "…" + string(runes[len(runes)-(max-1):])
}

func renderDetailPane(state aggregator.WorktreeState, now time.Time, height int) string {
	if state.Worktree.Path == "" {
		return ""
	}
	wt := state.Worktree

	var sb strings.Builder
	sb.Grow(512)
	branch := FormatBranch(wt.Branch, wt.Detached)
	sb.WriteString(detailHeaderStyle.Render(truncate(branch, detailContentW)))
	sb.WriteString("\n")
	sb.WriteString(detailLabelStyle.Render(elidePath(wt.Path, detailContentW)))
	sb.WriteString("\n\n")

	row := func(label, value string) {
		if value == "" {
			return
		}
		sb.WriteString(detailLabelStyle.Render(fmt.Sprintf("%-*s", labelW, label)))
		sb.WriteString(value)
		sb.WriteString("\n")
	}

	if wt.LastCommit.Subject != "" {
		row("commit", detailValueStyle.Render(truncate(wt.LastCommit.Subject, valueW)))
	}
	row("age", detailValueStyle.Render(FormatRelativeTime(wt.LastCommit.When, now)))
	if wt.HasUpstream {
		row("upstream", detailValueStyle.Render(FormatAheadBehind(wt.Ahead, wt.Behind, true)))
	} else {
		row("upstream", detailLabelStyle.Render("(none)"))
	}
	row("dirty", detailValueStyle.Render(dirtyCountString(wt.DirtyFiles)))

	if state.PR != nil {
		sb.WriteString("\n")
		sb.WriteString(detailHeaderStyle.Render("PR"))
		sb.WriteString("\n")
		row("number", detailValueStyle.Render(fmt.Sprintf("#%d", state.PR.Number)))
		row("title", detailValueStyle.Render(truncate(state.PR.Title, valueW)))
		row("state", prDetailState(*state.PR))
		row("ci", prDetailCI(state.PR.CIRollup))
		row("review", prDetailReview(state.PR.ReviewState))
		if state.PRStale {
			sb.WriteString(prStaleStyle.Render("(stale)"))
			sb.WriteString("\n")
		}
	}

	if len(state.Procs) > 0 {
		sb.WriteString("\n")
		sb.WriteString(detailHeaderStyle.Render("Processes"))
		sb.WriteString("\n")
		for _, p := range state.Procs {
			line := fmt.Sprintf("%-7d %s", p.Pid, truncate(p.Command, detailContentW-8))
			if isClaudeProc(p.Command, p.Args) {
				line = procsClaudeStyle.Render(line)
			} else {
				line = detailValueStyle.Render(line)
			}
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	if state.Live != nil || hasRecentTopLevel(state.Recent) {
		sb.WriteString("\n")
		sb.WriteString(detailHeaderStyle.Render("Sessions"))
		sb.WriteString("\n")
		if state.Live != nil {
			sb.WriteString(liveStyle.Render("●"))
			sb.WriteString(" ")
			sb.WriteString(detailValueStyle.Render(state.Live.Model))
			sb.WriteString("  ")
			sb.WriteString(detailLabelStyle.Render(FormatRelativeTime(state.Live.UpdatedAt, now)))
			sb.WriteString("\n")
		}
		shown := 0
		for _, s := range state.Recent {
			if shown >= 2 {
				break
			}
			// Skip subagent sessions — they share the parent's model/cwd and
			// duplicate visually.
			if s.IsSidechain {
				continue
			}
			if state.Live != nil && s.ID == state.Live.ID {
				continue
			}
			sb.WriteString("  ")
			sb.WriteString(detailLabelStyle.Render(s.Model))
			sb.WriteString("  ")
			sb.WriteString(detailLabelStyle.Render(FormatRelativeTime(s.UpdatedAt, now)))
			sb.WriteString("\n")
			shown++
		}
	}

	style := detailBorderStyle
	if height > 0 {
		style = style.Height(height)
	}
	return style.Render(sb.String())
}

// hasRecentTopLevel reports whether the Recent list contains at least one
// non-sidechain session worth showing in the pane.
func hasRecentTopLevel(recent []*sessions.Session) bool {
	for _, s := range recent {
		if !s.IsSidechain {
			return true
		}
	}
	return false
}

func dirtyCountString(n int) string {
	if n == 0 {
		return "clean"
	}
	return fmt.Sprintf("%d file(s)", n)
}

func prDetailState(p pr.PR) string {
	switch {
	case p.State == "MERGED":
		return prStateMergedStyle.Render("merged")
	case p.State == "CLOSED":
		return prStateClosedStyle.Render("closed")
	case p.IsDraft:
		return prStateDraftStyle.Render("draft")
	default:
		return prStateOpenStyle.Render("open")
	}
}

func prDetailCI(rollup string) string {
	switch rollup {
	case "SUCCESS":
		return prCISuccessStyle.Render("✓ passing")
	case "FAILURE":
		return prCIFailureStyle.Render("✗ failing")
	case "PENDING":
		return prCIPendingStyle.Render("⋯ pending")
	default:
		return ""
	}
}

func prDetailReview(state string) string {
	switch state {
	case "APPROVED":
		return prReviewApprStyle.Render("approved")
	case "CHANGES_REQUESTED":
		return prReviewChangeStyle.Render("changes requested")
	case "REVIEW_REQUIRED":
		return prReviewReqStyle.Render("review required")
	default:
		return ""
	}
}

// layoutWithDetail joins the worktree list and the detail pane horizontally.
// If the terminal is too narrow, returns the list alone. The detail string
// already carries its own border + (optional) height; the list column is
// padded by JoinHorizontal to match.
func layoutWithDetail(list, detail string, width int) string {
	if width < detailPaneVisibleWidth || detail == "" {
		return list
	}
	listW := width - detailPaneWidth - 4
	if listW < 30 {
		return list
	}
	listBox := lipgloss.NewStyle().Width(listW).Render(list)
	detailBox := lipgloss.NewStyle().Width(detailPaneWidth).Render(detail)
	return lipgloss.JoinHorizontal(lipgloss.Top, listBox, "  ", detailBox)
}
