package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/analytics"
)

// forensicsView is the sub-tab enum within the forensics top-level tab.
// Zero value is viewSpend so an uninitialized Model starts on the spend view.
type forensicsView int

const (
	viewSpend forensicsView = iota
	viewSessions
	viewTools
	viewWorktrees
)

const forensicsViewCount = 4

// label returns the clean display label for the sub-tab bar.
// No digit prefixes — digits are documented in the footer, not painted here.
func (v forensicsView) label() string {
	return [...]string{"spend", "sessions", "tools", "worktrees"}[v]
}

// loadAnalyticsCmd returns a tea.Cmd that calls analytics.Build
// asynchronously. On success it dispatches AnalyticsLoadedMsg; on error
// or a nil store (test fakes are allowed to return nil from
// SessionStore) it dispatches an empty AnalyticsLoadedMsg, which the
// Update handler ignores so the existing snapshot (if any) is preserved.
func loadAnalyticsCmd(r Refresher, now time.Time) tea.Cmd {
	return func() tea.Msg {
		store := r.SessionStore()
		if store == nil {
			return AnalyticsLoadedMsg{}
		}
		snap, err := analytics.Build(store, now)
		if err != nil {
			return AnalyticsLoadedMsg{}
		}
		return AnalyticsLoadedMsg{Snapshot: snap}
	}
}

// updateForensicsMode handles key input while on the forensics tab.
// Digit keys 1-4 jump directly; h/l cycle with wrap-around; r re-triggers
// the analytics load; Tab and q fall through to the shared normal handler.
func (m Model) updateForensicsMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case '1':
			m.forensicsView = viewSpend
			return m, nil
		case '2':
			m.forensicsView = viewSessions
			return m, nil
		case '3':
			m.forensicsView = viewTools
			return m, nil
		case '4':
			m.forensicsView = viewWorktrees
			return m, nil
		case 'h':
			// Cycle backward with wrap-around.
			m.forensicsView = (m.forensicsView + forensicsViewCount - 1) % forensicsViewCount
			return m, nil
		case 'l':
			// Cycle forward with wrap-around.
			m.forensicsView = (m.forensicsView + 1) % forensicsViewCount
			return m, nil
		case keyQuit:
			return m, tea.Quit
		case keyRefresh:
			return m, loadAnalyticsCmd(m.refresher, m.now())
		}
	}
	if msg.Type == tea.KeyTab {
		m.tab = tabOperational
		return m, nil
	}
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	return m, nil
}

// renderForensicsView renders the forensics tab: title bar, sub-tab bar,
// body (empty state or view stub), and footer.
func (m Model) renderForensicsView() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	title := m.renderTitleBar(width)
	subTabBar := m.renderForensicsSubTabBar(width)
	body := m.renderForensicsBody(width)
	footer := m.renderForensicsFooter(width)

	// Count how many rows the fixed chrome takes so the body can be padded to
	// pin the footer at the bottom (same pattern as renderOperationalView).
	bodyTargetH := 0
	if m.height > 0 {
		chromeRows := wrappedRows(title, width) + 1 + // title + blank line
			wrappedRows(subTabBar, width) + 1 + // sub-tab bar + blank line
			1 + // blank line before footer
			wrappedRows(footer, width)
		bodyTargetH = m.height - chromeRows
		if bodyTargetH < 0 {
			bodyTargetH = 0
		}
	}

	var pad string
	if bodyTargetH > 0 {
		if extra := bodyTargetH - wrappedRows(body, width); extra > 0 {
			pad = strings.Repeat("\n", extra)
		}
	}

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("\n\n")
	sb.WriteString(subTabBar)
	sb.WriteString("\n\n")
	sb.WriteString(body)
	sb.WriteString(pad)
	sb.WriteString("\n\n")
	sb.WriteString(footer)
	return sb.String()
}

// renderForensicsSubTabBar renders the horizontal sub-tab row:
// "  spend · sessions · tools · worktrees" with the active label in
// tabActive style and inactive labels in dimStyle. Separators use ruleStyle.
func (m Model) renderForensicsSubTabBar(width int) string {
	_ = width // reserved for future truncation
	views := [forensicsViewCount]forensicsView{viewSpend, viewSessions, viewTools, viewWorktrees}
	sep := " " + ruleStyle.Render("·") + " "

	var parts []string
	for _, v := range views {
		if v == m.forensicsView {
			parts = append(parts, tabActive.Render(v.label()))
		} else {
			parts = append(parts, dimStyle.Render(v.label()))
		}
	}
	return "  " + strings.Join(parts, sep)
}

// renderForensicsBody renders the body area. When the snapshot has no
// sessions the empty-state placeholder is returned. Otherwise dispatches
// to the renderer for the active sub-view.
func (m Model) renderForensicsBody(width int) string {
	if len(m.analytics.Sessions) == 0 {
		return dimStyle.Render("  (no sessions yet)")
	}
	now := m.now()
	switch m.forensicsView {
	case viewSpend:
		return renderSpendView(m.analytics.Days, m.analytics.WindowStart, m.analytics.WindowEnd, width)
	case viewSessions:
		return renderSessionsView(m.analytics.Sessions, now, width)
	case viewTools:
		return renderToolsView(m.analytics.Tools, m.analytics.Sessions, width)
	case viewWorktrees:
		return renderWorktreesView(m.analytics.Worktrees, now, width)
	default:
		return dimStyle.Render("  (" + m.forensicsView.label() + " view)")
	}
}

func (m Model) renderForensicsFooter(width int) string {
	help := keyStyle.Render("[tab]") + " " + keyDescStyle.Render("back to ops") + "  " +
		keyDescStyle.Render("·") + "  " +
		keyStyle.Render("[1-4]") + " " + keyDescStyle.Render("view") + "  " +
		keyDescStyle.Render("·") + "  " +
		keyStyle.Render("[h/l]") + " " + keyDescStyle.Render("prev/next") + "  " +
		keyDescStyle.Render("·") + "  " +
		keyStyle.Render("[r]") + " " + keyDescStyle.Render("refresh") + "  " +
		keyDescStyle.Render("·") + "  " +
		keyStyle.Render("[q]") + " " + keyDescStyle.Render("quit")

	helpW := lipgloss.Width(help)
	fill := width - helpW - 4
	if fill < 1 {
		fill = 1
	}
	return "  " + help + " " + ruleStyle.Render(strings.Repeat("─", fill)) + "  "
}
