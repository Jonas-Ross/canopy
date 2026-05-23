package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/analytics"
)

// loadAnalyticsCmd returns a tea.Cmd that calls analytics.Build
// asynchronously. On success it dispatches AnalyticsLoadedMsg; on error
// it dispatches an empty AnalyticsLoadedMsg (the loaded flag is not set
// and the placeholder view renders until the next refresh).
func loadAnalyticsCmd(r Refresher, now time.Time) tea.Cmd {
	return func() tea.Msg {
		snap, err := analytics.Build(r.SessionStore(), now)
		if err != nil {
			return AnalyticsLoadedMsg{}
		}
		return AnalyticsLoadedMsg{Snapshot: snap}
	}
}

// renderForensicsView renders the forensics tab placeholder: title bar (with
// forensics active), empty body padded to height, and a minimal footer hint.
// Task 10 will wire in real sub-tab navigation.
func (m Model) renderForensicsView() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	title := m.renderTitleBar(width)
	footer := m.renderForensicsFooter(width)

	// Pin footer to the bottom by the same height-padding pattern used in
	// renderOperationalView so the layout has a stable row count on tab switch.
	bodyTargetH := 0
	if m.height > 0 {
		bodyTargetH = m.height - wrappedRows(title, width) - 1 - 1 - wrappedRows(footer, width)
	}

	var pad string
	if bodyTargetH > 0 {
		pad = strings.Repeat("\n", bodyTargetH)
	}

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("\n\n")
	sb.WriteString(pad)
	sb.WriteString("\n\n")
	sb.WriteString(footer)
	return sb.String()
}

func (m Model) renderForensicsFooter(width int) string {
	help := keyStyle.Render("[tab]") + " " + keyDescStyle.Render("back to ops") + "  " +
		keyDescStyle.Render("·") + "  " +
		keyStyle.Render("[q]") + " " + keyDescStyle.Render("quit")

	helpW := lipgloss.Width(help)
	fill := width - helpW - 4
	if fill < 1 {
		fill = 1
	}
	return "  " + help + " " + ruleStyle.Render(strings.Repeat("─", fill)) + "  "
}
