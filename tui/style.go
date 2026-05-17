package tui

import "github.com/charmbracelet/lipgloss"

// Adaptive palette — ANSI 16 codes so it inherits the user's terminal theme.
var (
	colFG     = lipgloss.AdaptiveColor{Light: "0", Dark: "15"}
	colDim    = lipgloss.AdaptiveColor{Light: "8", Dark: "8"}
	colGreen  = lipgloss.AdaptiveColor{Light: "2", Dark: "10"}
	colRed    = lipgloss.AdaptiveColor{Light: "1", Dark: "9"}
	colYellow = lipgloss.AdaptiveColor{Light: "3", Dark: "11"}
	colBlue   = lipgloss.AdaptiveColor{Light: "4", Dark: "12"}
	colCyan   = lipgloss.AdaptiveColor{Light: "6", Dark: "14"}
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colFG)
	repoStyle  = lipgloss.NewStyle().Foreground(colCyan)
	ruleStyle  = lipgloss.NewStyle().Foreground(colDim)
	tabActive  = lipgloss.NewStyle().Foreground(colCyan).Bold(true)
	tabFaded   = lipgloss.NewStyle().Foreground(colDim)

	branchStyle   = lipgloss.NewStyle().Foreground(colFG)
	detachedStyle = lipgloss.NewStyle().Italic(true).Foreground(colDim)
	dirtyStyle    = lipgloss.NewStyle().Foreground(colYellow)
	aheadStyle    = lipgloss.NewStyle().Foreground(colGreen)
	behindStyle   = lipgloss.NewStyle().Foreground(colRed)
	syncStyle     = lipgloss.NewStyle().Foreground(colDim)
	ageStyle      = lipgloss.NewStyle().Foreground(colDim)
	modelStyle    = lipgloss.NewStyle().Foreground(colDim)
	liveStyle     = lipgloss.NewStyle().Foreground(colGreen).Bold(true)

	focusCursor   = lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	focusedBranch = lipgloss.NewStyle().Foreground(colFG).Bold(true)

	keyStyle     = lipgloss.NewStyle().Foreground(colFG).Bold(true)
	keyDescStyle = lipgloss.NewStyle().Foreground(colDim)
	dimStyle     = lipgloss.NewStyle().Foreground(colDim)
)
