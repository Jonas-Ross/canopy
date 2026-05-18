package tui

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
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

	top := renderDetailTop(state.Worktree, now)
	prSec := renderDetailPR(state)
	sessionsSec := renderDetailSessions(state, now)

	// Procs is the only unbounded section; budget it so the pane fits
	// within height. -1 means unconstrained.
	procsBudget := -1
	if height > 0 && len(state.Procs) > 0 {
		nSections := 2 // top + procs
		used := lipgloss.Height(top)
		if prSec != "" {
			nSections++
			used += lipgloss.Height(prSec)
		}
		if sessionsSec != "" {
			nSections++
			used += lipgloss.Height(sessionsSec)
		}
		procsBudget = height - used - (nSections - 1)
		if procsBudget < 1 {
			procsBudget = 1
		}
	}
	procsSec := renderDetailProcs(state.Procs, procsBudget)

	sections := make([]string, 0, 4)
	sections = append(sections, top)
	if prSec != "" {
		sections = append(sections, prSec)
	}
	if procsSec != "" {
		sections = append(sections, procsSec)
	}
	if sessionsSec != "" {
		sections = append(sections, sessionsSec)
	}
	body := strings.Join(sections, "\n\n")

	style := detailBorderStyle
	if height > 0 {
		// MaxHeight backstops the case where the fixed sections alone
		// already exceed height (tiny terminal); the procs budget can't
		// guard that on its own.
		style = style.Height(height).MaxHeight(height)
	}
	return style.Render(body)
}

func writeDetailRow(sb *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	sb.WriteByte('\n')
	sb.WriteString(detailLabelStyle.Render(fmt.Sprintf("%-*s", labelW, label)))
	sb.WriteString(value)
}

func renderDetailTop(wt git.Worktree, now time.Time) string {
	var sb strings.Builder
	sb.Grow(256)
	branch := FormatBranch(wt.Branch, wt.Detached)
	sb.WriteString(detailHeaderStyle.Render(truncate(branch, detailContentW)))
	sb.WriteByte('\n')
	sb.WriteString(detailLabelStyle.Render(elidePath(wt.Path, detailContentW)))
	sb.WriteByte('\n')

	if wt.LastCommit.Subject != "" {
		writeDetailRow(&sb, "commit", detailValueStyle.Render(truncate(wt.LastCommit.Subject, valueW)))
	}
	writeDetailRow(&sb, "age", detailValueStyle.Render(FormatRelativeTime(wt.LastCommit.When, now)))
	if wt.HasUpstream {
		writeDetailRow(&sb, "upstream", detailValueStyle.Render(FormatAheadBehind(wt.Ahead, wt.Behind, true)))
	} else {
		writeDetailRow(&sb, "upstream", detailLabelStyle.Render("(none)"))
	}
	writeDetailRow(&sb, "dirty", detailValueStyle.Render(dirtyCountString(wt.DirtyFiles)))
	return sb.String()
}

func renderDetailPR(state aggregator.WorktreeState) string {
	if state.PR == nil {
		return ""
	}
	var sb strings.Builder
	sb.Grow(192)
	sb.WriteString(detailHeaderStyle.Render("PR"))

	writeDetailRow(&sb, "number", detailValueStyle.Render(fmt.Sprintf("#%d", state.PR.Number)))
	writeDetailRow(&sb, "title", detailValueStyle.Render(truncate(state.PR.Title, valueW)))
	writeDetailRow(&sb, "state", prDetailState(*state.PR))
	writeDetailRow(&sb, "ci", prDetailCI(state.PR.CIRollup))
	writeDetailRow(&sb, "review", prDetailReview(state.PR.ReviewState))
	if state.PRStale {
		sb.WriteByte('\n')
		sb.WriteString(prStaleStyle.Render("(stale)"))
	}
	return sb.String()
}

// renderDetailProcs caps the section so its visible rows fit `budget` (header
// + procs + optional "+N more"). budget <= 0 means unbounded.
func renderDetailProcs(list []procs.Process, budget int) string {
	if len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.Grow(64 + 32*len(list))
	sb.WriteString(detailHeaderStyle.Render("Processes"))

	show := len(list)
	hidden := 0
	if budget > 0 {
		maxRows := budget - 1 // header takes one row
		if maxRows < 0 {
			maxRows = 0
		}
		if show > maxRows {
			show = maxRows - 1 // reserve one row for "+N more"
			if show < 0 {
				show = 0
			}
			hidden = len(list) - show
		}
	}
	for i := 0; i < show; i++ {
		p := list[i]
		sb.WriteByte('\n')
		line := fmt.Sprintf("%-7d %s", p.Pid, truncate(p.Command, detailContentW-8))
		if isClaudeProc(p.Command, p.Args) {
			line = procsClaudeStyle.Render(line)
		} else {
			line = detailValueStyle.Render(line)
		}
		sb.WriteString(line)
	}
	if hidden > 0 {
		sb.WriteByte('\n')
		sb.WriteString(detailLabelStyle.Render(fmt.Sprintf("+%d more", hidden)))
	}
	return sb.String()
}

func renderDetailSessions(state aggregator.WorktreeState, now time.Time) string {
	if state.Live == nil && !hasRecentTopLevel(state.Recent) {
		return ""
	}
	var sb strings.Builder
	sb.Grow(128)
	sb.WriteString(detailHeaderStyle.Render("Sessions"))
	if state.Live != nil {
		sb.WriteByte('\n')
		sb.WriteString(liveStyle.Render("●"))
		sb.WriteByte(' ')
		sb.WriteString(detailValueStyle.Render(state.Live.Model))
		sb.WriteString("  ")
		sb.WriteString(detailLabelStyle.Render(FormatRelativeTime(state.Live.UpdatedAt, now)))
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
		sb.WriteByte('\n')
		sb.WriteString("  ")
		sb.WriteString(detailLabelStyle.Render(s.Model))
		sb.WriteString("  ")
		sb.WriteString(detailLabelStyle.Render(FormatRelativeTime(s.UpdatedAt, now)))
		shown++
	}
	return sb.String()
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
