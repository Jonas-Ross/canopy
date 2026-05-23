package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/jonasross/canopy/analytics"
)

const topToolsPerModel = 5 // show top N tools before "other"

// renderToolsView renders the tools sub-view: one section per model, with
// a colored type tag, simplified tool name, proportional bar, count, and
// percent for each of the top 5 tools, plus a dim "other" rollup row.
//
// sessionCountByModel must reflect the FULL analytics window — passing
// the length-capped Snapshot.Sessions here would undercount models
// whose session counts exceed recentSessionsLimit.
func renderToolsView(tools []analytics.ToolUsage, sessionCountByModel map[string]int) string {
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
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)

	var sb strings.Builder
	sb.Grow(1024)

	for mi, model := range models {
		if mi > 0 {
			sb.WriteByte('\n')
		}
		rows := byModel[model]

		totalCalls := 0
		for _, r := range rows {
			totalCalls += r.count
		}

		sessCount := sessionCountByModel[model]
		sb.WriteString("  ")
		sb.WriteString(tabActive.Render(model))
		sb.WriteString(dimStyle.Render(fmt.Sprintf("    %d sessions · %s calls",
			sessCount, formatWithCommas(totalCalls))))
		sb.WriteByte('\n')

		topN := topToolsPerModel
		if topN > len(rows) {
			topN = len(rows)
		}
		topRows := rows[:topN]
		otherRows := rows[topN:]

		for _, r := range topRows {
			sb.WriteString(renderToolRow(r.tool, r.count, totalCalls))
		}
		if len(otherRows) > 0 {
			otherCount := 0
			for _, r := range otherRows {
				otherCount += r.count
			}
			sb.WriteString(renderToolRow("other", otherCount, totalCalls))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderToolRow renders a single tool row including indent, type tag,
// formatted name, proportional bar, count, and percent. The "other"
// rollup row passes name="other" and gets the dim · tag automatically.
func renderToolRow(name string, count, totalCalls int) string {
	const (
		nameColW = 20
		barW     = 20
	)
	tag, tagStyle := categorizeTool(name)
	display := formatToolName(name, nameColW)
	fill, track := proportionalBar(count, totalCalls, barW)
	pct := 0
	if totalCalls > 0 {
		pct = (count * 100) / totalCalls
	}

	var sb strings.Builder
	sb.WriteString("    ") // 4-sp row indent
	sb.WriteString(tagStyle.Render(fmt.Sprintf("%-4s", tag)))
	sb.WriteString("  ")
	sb.WriteString(fmt.Sprintf("%-*s", nameColW, display))
	sb.WriteString("  ")
	sb.WriteString(fill)
	sb.WriteString(track)
	sb.WriteString("  ")
	sb.WriteString(fmt.Sprintf("%6d", count))
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%3d%%", pct)))
	sb.WriteByte('\n')
	return sb.String()
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
	// > 0 would fire on float-epsilon noise after exact ratios — e.g.
	// the math floor inside count/totalCalls*cellWidth can leave a sub-
	// epsilon residue that paints a phantom partial cell. 1e-9 is well
	// above IEEE 754 noise (~1e-17) and well below any real fraction
	// (smallest real frac is 1/cellWidth = 0.05 for our 20-cell bar).
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
	fillVisualW := utf8.RuneCountInString(raw.String())
	if fillVisualW > cellWidth {
		// count > totalCalls shouldn't happen but we promise the fill+track
		// width invariant — clamp both the visible string and the measurement
		// rather than letting a bad input corrupt the row layout.
		raw.Reset()
		raw.WriteString(strings.Repeat("█", cellWidth))
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
