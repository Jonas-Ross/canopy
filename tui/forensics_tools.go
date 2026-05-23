package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jonasross/canopy/analytics"
)

const (
	toolBarWidth    = 20 // max cells for bar in tools view
	topToolsPerModel = 5 // show top N tools before "other"
)

// renderToolsView renders the tools sub-view: per-model section header,
// top-5 tool bars, and an "other" row collapsing the remainder.
func renderToolsView(tools []analytics.ToolUsage, sessions []analytics.SessionSummary, width int) string {
	if len(tools) == 0 {
		return dimStyle.Render("  no tool data")
	}

	// Group tools by model. Collect models in sorted order.
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

	// Build session-count and total-call summaries per model.
	sessionCountByModel := make(map[string]int)
	for _, s := range sessions {
		sessionCountByModel[s.Model]++
	}

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
