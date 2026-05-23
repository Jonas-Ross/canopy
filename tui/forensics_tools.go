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
	// tools is (Model asc, Count desc, Tool asc) per analytics.ToolDistribution,
	// so per-model slices land here count-descending already — rows[:topN]
	// below is the top N without an extra sort.
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
		sb.WriteString(tabActive.Render(prettyModelName(model)))
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

// renderToolRow renders a single tool row. The "other" rollup row
// passes name="other" and gets the dim · tag automatically via
// categorizeTool's default.
func renderToolRow(name string, count, totalCalls int) string {
	const (
		nameColW = 20
		barW     = 20
	)
	tag, tagStyle := categorizeTool(name)
	display := formatToolName(name, nameColW)
	fill, track := proportionalBar(count, totalCalls, barW, tagStyle)
	pct := 0
	if totalCalls > 0 {
		pct = (count * 100) / totalCalls
	}

	var sb strings.Builder
	sb.WriteString("    ")
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
// forensics tools view. Unknown tools fall through to the dim · tag.
// Adding a category = one switch entry here + matching style in style.go.
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

// formatToolName turns mcp__<server>__<action> into <server>/<action>,
// collapsing multi-segment servers ("plugin_github_github") to their
// last _-separated segment. Non-MCP names pass through unchanged.
//
// When the joined form exceeds maxWidth, the action is middle-truncated
// with "…" and the server is preserved — never the other way around. A
// maxWidth of 0 or less disables truncation.
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

// proportionalBar returns the bar fill and the trailing space padding
// for a row, sized so fill+track is exactly cellWidth visual cells.
// The caller picks fillStyle — the tools view passes the row's category
// tag style so tag and bar share a hue.
//
// Floors a non-zero count to a single "▏" cell so tiny tools don't
// disappear. count == 0 or totalCalls == 0 renders no fill at all.
func proportionalBar(count, totalCalls, cellWidth int, fillStyle lipgloss.Style) (fill, track string) {
	if cellWidth <= 0 {
		return "", ""
	}
	if count <= 0 || totalCalls <= 0 {
		return "", strings.Repeat(" ", cellWidth)
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
	fill = fillStyle.Render(raw.String())
	trackW := cellWidth - fillVisualW
	if trackW > 0 {
		track = strings.Repeat(" ", trackW)
	}
	return fill, track
}

// prettyModelName turns "claude-opus-4-7" into "Opus 4.7" and
// "claude-haiku-4-5-20251001" into "Haiku 4.5" (date suffix dropped).
// Non-claude or malformed inputs pass through unchanged so unknown
// model identifiers stay visible rather than getting mangled.
func prettyModelName(name string) string {
	rest, ok := strings.CutPrefix(name, "claude-")
	if !ok {
		return name
	}
	parts := strings.Split(rest, "-")
	if len(parts) < 3 {
		return name
	}
	family, major, minor := parts[0], parts[1], parts[2]
	if family == "" || major == "" || minor == "" {
		return name
	}
	return strings.ToUpper(family[:1]) + family[1:] + " " + major + "." + minor
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
