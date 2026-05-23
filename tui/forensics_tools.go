package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jonasross/canopy/analytics"
)

const (
	toolBarWidth    = 20 // max cells for bar in tools view
	topToolsPerModel = 5 // show top N tools before "other"
)

// renderToolsView renders the tools sub-view: per-model section header,
// top-5 tool bars, and an "other" row collapsing the remainder.
// sessionCountByModel must reflect the FULL analytics window — passing the
// length-capped Snapshot.Sessions here would undercount models whose
// session counts exceed recentSessionsLimit.
func renderToolsView(tools []analytics.ToolUsage, sessionCountByModel map[string]int, width int) string {
	if len(tools) == 0 {
		return dimStyle.Render("  no tool data")
	}

	type toolRow struct {
		tool  string
		count int
	}
	byModel := make(map[string][]toolRow)
	for _, t := range tools {
		byModel[t.Model] = append(byModel[t.Model], toolRow{tool: t.Tool, count: t.Count})
	}
	// Tools are already sorted (Model asc, Count desc) per Snapshot contract,
	// so we just need the sorted model list.
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)

	var sb strings.Builder
	sb.Grow(512)

	for mi, model := range models {
		if mi > 0 {
			sb.WriteByte('\n')
		}
		rows := byModel[model]

		// Compute total calls for this model.
		totalCalls := 0
		for _, r := range rows {
			totalCalls += r.count
		}

		sessCount := sessionCountByModel[model]
		header := fmt.Sprintf("%s    %d sessions · %s calls",
			model,
			sessCount,
			formatWithCommas(totalCalls),
		)
		sb.WriteString("  ")
		sb.WriteString(dimStyle.Render(header))
		sb.WriteByte('\n')

		// Top N tools + "other" collapse.
		topN := topToolsPerModel
		if topN > len(rows) {
			topN = len(rows)
		}
		topRows := rows[:topN]
		otherRows := rows[topN:]

		// Find max count among displayed rows for normalization.
		maxCount := 0
		for _, r := range topRows {
			if r.count > maxCount {
				maxCount = r.count
			}
		}

		for _, r := range topRows {
			bar := toolBar(r.count, maxCount)
			pct := 0
			if totalCalls > 0 {
				pct = (r.count * 100) / totalCalls
			}
			sb.WriteString("    ")
			sb.WriteString(dimStyle.Render(fmt.Sprintf("%-8s", r.tool)))
			sb.WriteString(bar)
			sb.WriteString("  ")
			sb.WriteString(dimStyle.Render(fmt.Sprintf("%5d  %3d%%", r.count, pct)))
			sb.WriteByte('\n')
		}

		// "other" row.
		if len(otherRows) > 0 {
			otherCount := 0
			for _, r := range otherRows {
				otherCount += r.count
			}
			bar := toolBar(otherCount, maxCount)
			pct := 0
			if totalCalls > 0 {
				pct = (otherCount * 100) / totalCalls
			}
			sb.WriteString("    ")
			sb.WriteString(dimStyle.Render(fmt.Sprintf("%-8s", "other")))
			sb.WriteString(bar)
			sb.WriteString("  ")
			sb.WriteString(dimStyle.Render(fmt.Sprintf("%5d  %3d%%", otherCount, pct)))
			sb.WriteByte('\n')
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// toolBar returns a filled bar of up to toolBarWidth cells, normalized over
// maxCount. Uses full-block (█) character. The cells cap matters for the
// "other" rollup row: that row's count is the sum of all non-top-5 tools
// and can exceed maxCount (which is the single highest individual top-5
// count), which would otherwise produce a negative pad width and panic.
func toolBar(count, maxCount int) string {
	if maxCount == 0 || count == 0 {
		return dimStyle.Render("▏" + strings.Repeat(" ", toolBarWidth-1))
	}
	cells := (count * toolBarWidth) / maxCount
	if cells < 1 {
		cells = 1
	}
	if cells > toolBarWidth {
		cells = toolBarWidth
	}
	bar := strings.Repeat("█", cells)
	pad := strings.Repeat(" ", toolBarWidth-cells)
	return tabActive.Render(bar) + dimStyle.Render(pad)
}

// categorizeTool maps a tool name to its (tag, style) pair for the
// forensics tools view. The tag is always exactly 4 cells wide once
// rendered (padded to 4 by the caller via fmt.Sprintf("%-4s", tag)).
// Unknown tools fall through to ("·", toolTagDimStyle).
//
// Adding a new tool category is a single switch entry below plus a
// matching style in tui/style.go.
func categorizeTool(name string) (tag string, style lipgloss.Style) {
	if strings.HasPrefix(name, "mcp__") {
		return "mcp", toolTagMCPStyle
	}
	switch name {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "Grep", "Glob":
		return "file", toolTagFileStyle
	case "Bash", "KillShell", "BashOutput", "Monitor":
		return "exec", toolTagExecStyle
	case "WebFetch", "WebSearch":
		return "web", toolTagWebStyle
	case "Task", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet",
		"TaskOutput", "TaskStop", "SendMessage", "Skill", "ToolSearch":
		return "task", toolTagTaskStyle
	}
	return "·", toolTagDimStyle
}

// formatToolName returns the display form of a tool name for the
// forensics tools view.
//
// Non-MCP names pass through unchanged.
//
// MCP names (mcp__<server>__<action>) are simplified:
//
//   - The mcp__ prefix is stripped.
//   - The server portion is split on "_" and reduced to its LAST
//     segment. This collapses wrapper-noise like "plugin_github_github"
//     to "github" while leaving single-segment servers ("semble",
//     "wiki") untouched.
//   - The action segment is kept intact.
//   - The two parts are joined with "/".
//
// If the joined form exceeds maxWidth visual cells, the action portion
// is middle-truncated with "…" while the server portion is preserved.
// In the edge case where the simplified server alone is >= maxWidth,
// the server is kept and the action becomes "…" (per spec — the server
// is never truncated).
//
// maxWidth <= 0 disables truncation.
func formatToolName(name string, maxWidth int) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	rest := strings.TrimPrefix(name, "mcp__")
	idx := strings.Index(rest, "__")
	var server, action string
	if idx < 0 {
		server, action = rest, ""
	} else {
		server, action = rest[:idx], rest[idx+2:]
	}
	if i := strings.LastIndex(server, "_"); i >= 0 {
		server = server[i+1:]
	}
	if action == "" {
		return server
	}
	out := server + "/" + action
	if maxWidth <= 0 || len(out) <= maxWidth {
		return out
	}
	// len() returns bytes; this is correct for ASCII tool names (every
	// MCP tool name observed in production is an ASCII identifier).
	// The single "…" we add later is 1 visual cell but 3 bytes, which
	// the budget subtraction below accounts for.
	prefix := server + "/"
	if len(prefix) >= maxWidth {
		return prefix + "…"
	}
	budget := maxWidth - len(prefix) - 1 // -1 for "…"
	if budget < 1 {
		return prefix + "…"
	}
	head := (budget * 3) / 4
	tail := budget - head
	if head < 1 {
		head = 1
		tail = budget - 1
	}
	if tail < 1 {
		tail = 0
	}
	return prefix + action[:head] + "…" + action[len(action)-tail:]
}

// horizontalBlocks are the unicode left-block glyphs used for the trailing
// partial cell of a proportional bar, from lightest (1/8 cell) to fullest
// (7/8 cell). Index 0 maps to the smallest visible fill; the full block
// "█" is rendered separately (see proportionalBar).
var horizontalBlocks = []rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// proportionalBar returns the bar fill and dim track for a row, padded
// to exactly cellWidth visual cells total.
//
// Layout: zero or more "█" cells (cyan via barFillStyle) optionally
// followed by ONE partial-block glyph (also cyan); the remainder is
// "░" cells (dim via dimStyle). Visual width of fill+track == cellWidth.
//
// Special cases:
//   - count == 0 (regardless of total): no fill, full-width dim track.
//   - count > 0 but ratio rounds to zero cells: a single "▏" fill cell
//     ("present but tiny" beats invisible).
//
// totalCalls == 0 is treated as no-data — same as count == 0.
func proportionalBar(count, totalCalls, cellWidth int) (fill, track string) {
	if cellWidth <= 0 {
		return "", ""
	}
	if count <= 0 || totalCalls <= 0 {
		return "", dimStyle.Render(strings.Repeat("░", cellWidth))
	}
	pos := float64(count) / float64(totalCalls) * float64(cellWidth)
	fullCells := int(pos)
	frac := pos - float64(fullCells)
	var raw strings.Builder
	if fullCells > 0 {
		raw.WriteString(strings.Repeat("█", fullCells))
	}
	if frac > 1e-9 && fullCells < cellWidth {
		idx := int(frac * float64(len(horizontalBlocks)))
		if idx >= len(horizontalBlocks) {
			idx = len(horizontalBlocks) - 1
		}
		raw.WriteRune(horizontalBlocks[idx])
	}
	// 1-cell floor: count > 0 but math produced an empty fill.
	if raw.Len() == 0 {
		raw.WriteRune('▏')
	}
	fillVisualW := 0
	for range raw.String() {
		fillVisualW++
	}
	if fillVisualW > cellWidth {
		fillVisualW = cellWidth
	}
	fill = barFillStyle.Render(raw.String())
	trackW := cellWidth - fillVisualW
	if trackW > 0 {
		track = dimStyle.Render(strings.Repeat("░", trackW))
	}
	return fill, track
}

// formatWithCommas formats an integer with comma thousands separators.
func formatWithCommas(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
