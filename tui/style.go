package tui

import "github.com/charmbracelet/lipgloss"

// Adaptive palette — ANSI 16 codes so it inherits the user's terminal theme.
var (
	colFG      = lipgloss.AdaptiveColor{Light: "0", Dark: "15"}
	colDim     = lipgloss.AdaptiveColor{Light: "8", Dark: "8"}
	colDimmer  = lipgloss.AdaptiveColor{Light: "7", Dark: "238"}
	colGreen   = lipgloss.AdaptiveColor{Light: "2", Dark: "10"}
	colRed     = lipgloss.AdaptiveColor{Light: "1", Dark: "9"}
	colYellow  = lipgloss.AdaptiveColor{Light: "3", Dark: "11"}
	colBlue    = lipgloss.AdaptiveColor{Light: "4", Dark: "12"}
	colCyan    = lipgloss.AdaptiveColor{Light: "6", Dark: "14"}
	colMagenta = lipgloss.AdaptiveColor{Light: "5", Dark: "13"}
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colFG)
	repoStyle  = lipgloss.NewStyle().Foreground(colCyan)
	ruleStyle  = lipgloss.NewStyle().Foreground(colDim)
	tabActive  = lipgloss.NewStyle().Foreground(colCyan).Bold(true)
	tabFaded   = lipgloss.NewStyle().Foreground(colDim)

	branchStyle    = lipgloss.NewStyle().Foreground(colFG)
	mergedStyle    = lipgloss.NewStyle().Foreground(colDimmer)
	detachedStyle  = lipgloss.NewStyle().Italic(true).Foreground(colDim)
	dirtyStyle     = lipgloss.NewStyle().Foreground(colYellow)
	aheadStyle     = lipgloss.NewStyle().Foreground(colGreen)
	behindStyle    = lipgloss.NewStyle().Foreground(colRed)
	syncStyle      = lipgloss.NewStyle().Foreground(colDim)
	ageStyle       = lipgloss.NewStyle().Foreground(colDim)
	modelStyle     = lipgloss.NewStyle().Foreground(colDim)
	liveStyle = lipgloss.NewStyle().Foreground(colGreen).Bold(true)
	// liveDimStyle is the off-phase of the live-indicator blink: same green
	// glyph, no bold. The visible delta from liveStyle is brightness alone —
	// reads as a steady "alive, breathing" rhythm rather than an alert.
	// Background SGR codes must stay empty (Background(colGreen) on a green
	// glyph renders as a solid block — regression guarded in golden_test.go).
	liveDimStyle = lipgloss.NewStyle().Foreground(colGreen)

	// PR state
	prStateOpenStyle    = lipgloss.NewStyle().Foreground(colGreen)
	prStateDraftStyle   = lipgloss.NewStyle().Foreground(colDim)
	prStateMergedStyle  = lipgloss.NewStyle().Foreground(colMagenta)
	prStateClosedStyle  = lipgloss.NewStyle().Foreground(colRed)
	prStaleStyle        = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	prNumberStyle       = lipgloss.NewStyle().Foreground(colDim)
	prCISuccessStyle    = lipgloss.NewStyle().Foreground(colGreen)
	prCIFailureStyle    = lipgloss.NewStyle().Foreground(colRed)
	prCIPendingStyle    = lipgloss.NewStyle().Foreground(colYellow)
	prReviewApprStyle   = lipgloss.NewStyle().Foreground(colGreen)
	prReviewChangeStyle = lipgloss.NewStyle().Foreground(colRed)
	prReviewReqStyle    = lipgloss.NewStyle().Foreground(colYellow)

	procsStyle       = lipgloss.NewStyle().Foreground(colDim)
	procsClaudeStyle = lipgloss.NewStyle().Foreground(colBlue)

	focusCursor   = lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	focusedBranch = lipgloss.NewStyle().Foreground(colFG).Bold(true)

	detailHeaderStyle = lipgloss.NewStyle().Foreground(colFG).Bold(true)
	detailLabelStyle  = lipgloss.NewStyle().Foreground(colDim)
	detailValueStyle  = lipgloss.NewStyle().Foreground(colFG)
	detailBorderStyle = lipgloss.NewStyle().BorderForeground(colDim).BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).PaddingLeft(2)

	promptStyle = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	noticeStyle = lipgloss.NewStyle().Foreground(colCyan)

	keyStyle     = lipgloss.NewStyle().Foreground(colFG).Bold(true)
	keyDescStyle = lipgloss.NewStyle().Foreground(colDim)
	dimStyle     = lipgloss.NewStyle().Foreground(colDim)

	// Forensics tools view: per-category type-tag styles. Each renders
	// the 4-char lowercase tag (e.g. "file", "exec") AND is reused as
	// the proportional-bar fill color, so a row's tag and bar share a
	// hue — strong categorical signal without colorizing the name/count
	// columns. Non-bold deliberately: bold on block characters (█)
	// renders as a solid block on some terminals (see liveDimStyle).
	toolTagFileStyle = lipgloss.NewStyle().Foreground(colBlue)
	toolTagExecStyle = lipgloss.NewStyle().Foreground(colYellow)
	toolTagWebStyle  = lipgloss.NewStyle().Foreground(colMagenta)
	toolTagMCPStyle  = lipgloss.NewStyle().Foreground(colGreen)
	toolTagTaskStyle = lipgloss.NewStyle().Foreground(colCyan)
	toolTagDimStyle  = dimStyle // alias for the "·" / "other" tag

	// primaryMarkerStyle has to read at a glance on dark terminals — cyan
	// matches the repo-identity accent in the title bar, and bold compensates
	// for the small ⌂ glyph that otherwise vanishes against a low-contrast bg.
	primaryMarkerStyle = lipgloss.NewStyle().Foreground(colCyan).Bold(true)
)

// Per-column width wrappers hoisted to package scope so repaints don't
// allocate a fresh lipgloss.Style per cell. Branch column width varies and
// is built inline in renderRow.
var (
	statusColStyle = lipgloss.NewStyle().Width(statusColW)
	prColStyle     = lipgloss.NewStyle().Width(prColW)
	procsColStyle  = lipgloss.NewStyle().Width(procsColW)
	ageColStyle    = lipgloss.NewStyle().Width(ageColW).Inherit(ageStyle)
)
