package tui

import "github.com/charmbracelet/lipgloss"

// v1 palette — intentionally minimal. Full polish is deferred to M4.5.
var (
	// focusedStyle highlights the selected row with reverse video.
	focusedStyle = lipgloss.NewStyle().Reverse(true)

	// dimStyle is used for the help footer and secondary elements.
	dimStyle = lipgloss.NewStyle().Faint(true)

	// liveColor is adaptive so the indicator is legible on both light and
	// dark terminals without hardcoding a specific ANSI code.
	liveColor = lipgloss.AdaptiveColor{Light: "2", Dark: "10"} // green

	// liveStyle applies the live-indicator color.
	liveStyle = lipgloss.NewStyle().Foreground(liveColor)
)
