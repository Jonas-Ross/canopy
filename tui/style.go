package tui

import "github.com/charmbracelet/lipgloss"

var (
	focusedStyle = lipgloss.NewStyle().Reverse(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	// AdaptiveColor so the indicator is legible on light and dark terminals.
	liveColor = lipgloss.AdaptiveColor{Light: "2", Dark: "10"}
	liveStyle = lipgloss.NewStyle().Foreground(liveColor)
)
