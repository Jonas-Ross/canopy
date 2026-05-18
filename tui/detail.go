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

	// The procs section is the only unbounded block — every other section has
	// a small ceiling. When `height` is set (driven by m.height from
	// WindowSizeMsg), budget procs against everything else so the assembled
	// pane never grows past `height`. Without this, a worktree with many
	// processes (e.g. the primary on a busy machine) would push the body
	// past the terminal height and the alt-screen would scroll the title
	// bar and worktree list off the top.
	//
	// budget = total rows the procs section may occupy (header + rows +
	// optional "+N more"). -1 means "no constraint" (no WindowSizeMsg yet,
	// or zero-height test fixtures).
	procsBudget := -1
	if height > 0 && len(state.Procs) > 0 {
		nSections := 2 // top + procs
		used := lineCount(top)
		if prSec != "" {
			nSections++
			used += lineCount(prSec)
		}
		if sessionsSec != "" {
			nSections++
			used += lineCount(sessionsSec)
		}
		procsBudget = height - used - (nSections - 1)
		if procsBudget < 1 {
			procsBudget = 1
		}
	}
	procsSec := renderDetailProcs(state.Procs, procsBudget)

	sections := []string{top}
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
		// Height pads short content up to `height`; MaxHeight is the backstop
		// that prevents overflow when the fixed sections alone would already
		// exceed `height` (tiny terminal, dense PR/session metadata).
		style = style.Height(height).MaxHeight(height)
	}
	return style.Render(body)
}

// lineCount returns the visible row count of a section string, where each row
// ends with "\n" except the last (sections are built without trailing
// newlines). Empty strings count as zero rows.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// renderDetailTop renders the worktree header, path, and metadata rows
// (commit/age/upstream/dirty). Returns a string with no trailing newline.
func renderDetailTop(wt git.Worktree, now time.Time) string {
	var sb strings.Builder
	sb.Grow(256)
	branch := FormatBranch(wt.Branch, wt.Detached)
	sb.WriteString(detailHeaderStyle.Render(truncate(branch, detailContentW)))
	sb.WriteByte('\n')
	sb.WriteString(detailLabelStyle.Render(elidePath(wt.Path, detailContentW)))
	// Blank line between the path and the metadata rows.
	sb.WriteByte('\n')

	row := func(label, value string) {
		if value == "" {
			return
		}
		sb.WriteByte('\n')
		sb.WriteString(detailLabelStyle.Render(fmt.Sprintf("%-*s", labelW, label)))
		sb.WriteString(value)
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
	return sb.String()
}

// renderDetailPR renders the PR header + rows. Returns "" when no PR.
func renderDetailPR(state aggregator.WorktreeState) string {
	if state.PR == nil {
		return ""
	}
	var sb strings.Builder
	sb.Grow(192)
	sb.WriteString(detailHeaderStyle.Render("PR"))

	row := func(label, value string) {
		if value == "" {
			return
		}
		sb.WriteByte('\n')
		sb.WriteString(detailLabelStyle.Render(fmt.Sprintf("%-*s", labelW, label)))
		sb.WriteString(value)
	}
	row("number", detailValueStyle.Render(fmt.Sprintf("#%d", state.PR.Number)))
	row("title", detailValueStyle.Render(truncate(state.PR.Title, valueW)))
	row("state", prDetailState(*state.PR))
	row("ci", prDetailCI(state.PR.CIRollup))
	row("review", prDetailReview(state.PR.ReviewState))
	if state.PRStale {
		sb.WriteByte('\n')
		sb.WriteString(prStaleStyle.Render("(stale)"))
	}
	return sb.String()
}

// renderDetailProcs renders the Processes header + capped proc rows. When
// budget > 0 and len(list) would exceed budget, the section emits
// (budget-1) proc rows plus a "+N more" line so the total occupies exactly
// `budget` visible rows. budget <= 0 means render every proc unbounded.
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
		// 1 row for the header — the rest of the budget is split between
		// proc rows and (when truncating) the "+N more" line.
		maxRows := budget - 1
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

// renderDetailSessions renders the Sessions header + the live row + up to two
// recent non-sidechain rows. Returns "" when nothing to show.
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
