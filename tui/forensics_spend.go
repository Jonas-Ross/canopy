package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/analytics"
	"github.com/jonasross/canopy/sessions"
)

// sparkBlocks are the unicode block elements used for sparkline bars,
// from lowest to highest density.
var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

const sparkWidth = 12 // max cells per bar

// formatTokens formats a raw token count with K or M suffix for display.
// 0 → "0", 999 → "999", 1500 → "1.5K", 12_400_000 → "12.4M".
func formatTokens(n int) string {
	switch {
	case n == 0:
		return "0"
	case n < 1_000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// tokenTotal returns Input + Output + CacheRead + CacheCreation.
func tokenTotal(t sessions.TokenStats) int {
	return t.Input + t.Output + t.CacheRead + t.CacheCreation
}

// truncateToDayUTC normalizes t to UTC midnight. Zero-time stays zero.
func truncateToDayUTC(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// sparkBar returns a sparkline bar of width up to sparkWidth cells,
// normalized so that the bar with maxTotal fills the entire width.
// Returns a dim "·" when total == 0.
func sparkBar(total, maxTotal int) string {
	if maxTotal == 0 || total == 0 {
		return dimStyle.Render("·")
	}
	ratio := float64(total) / float64(maxTotal)
	cells := int(ratio * float64(sparkWidth))
	if cells < 1 {
		cells = 1
	}
	if cells > sparkWidth {
		cells = sparkWidth
	}
	// Pick the trailing glyph based on the fractional cell remainder.
	// When the ratio lands exactly on a cell boundary (frac == 0), use
	// the full block rather than wrapping back to the lightest one.
	cellPosition := ratio * float64(sparkWidth)
	frac := cellPosition - float64(int(cellPosition))
	glyphIdx := int(frac * float64(len(sparkBlocks)))
	if frac == 0 || cells >= sparkWidth {
		glyphIdx = len(sparkBlocks) - 1
	}
	if glyphIdx >= len(sparkBlocks) {
		glyphIdx = len(sparkBlocks) - 1
	}
	glyph := string(sparkBlocks[glyphIdx])
	// Full bar: (cells-1) full blocks + trailing glyph.
	full := strings.Repeat(string(sparkBlocks[len(sparkBlocks)-1]), cells-1)
	return tabActive.Render(full + glyph)
}

// renderSpendView renders the spend sub-view: header, one row per day in
// snap.Days (sorted DESC), zero-day placeholders, footer totals.
// width is the available terminal width.
func renderSpendView(days []analytics.DayBucket, windowStart, windowEnd time.Time, width int) string {
	// Determine the range of dates to display (fill gaps with zero rows).
	// We show every day from the most recent day with activity back to the
	// earliest, plus we fill any gaps inside that range.
	if len(days) == 0 {
		return dimStyle.Render("  no spend data in window")
	}

	// Build a date-indexed map for fast lookup.
	dayMap := make(map[string]analytics.DayBucket, len(days))
	for _, d := range days {
		dayMap[d.Date.Format("2006-01-02")] = d
	}

	// Anchor display range to the requested window — the header reads
	// "last 30 days", so the table needs to render that range with gap
	// rows for inactive days. Fall back to activity bounds when the
	// snapshot didn't populate the window (test fixtures may not).
	newest := truncateToDayUTC(windowEnd)
	oldest := truncateToDayUTC(windowStart)
	if newest.IsZero() {
		newest = days[0].Date
	}
	if oldest.IsZero() {
		oldest = days[len(days)-1].Date
	}

	// Find the max total across all active days for sparkline normalization.
	maxTotal := 0
	for _, d := range days {
		if t := tokenTotal(d.Tokens); t > maxTotal {
			maxTotal = t
		}
	}

	// Column widths (visual):
	//   date    : 10   "YYYY-MM-DD"
	//   gap     : 3
	//   bar     : sparkWidth + padding
	//   in      : 12   "in 12.4M"
	//   out     : 12   "out 3.2M"
	//   cache-r : 16   "cache-r 8.1M"
	//   cache-c : 16   "cache-c 1.2M"
	//   total   : 14   "total 24.9M"

	const (
		barCol    = sparkWidth + 2 // bar + 2 spaces padding
		numColW   = 12             // "in 99.9M" fits in 10, pad to 12
		cacheColW = 16
	)

	var sb strings.Builder
	sb.Grow(1024)

	header := fmt.Sprintf("  spend · last 30 days  filter: all")
	sb.WriteString(dimStyle.Render(header))
	sb.WriteByte('\n')

	var totIn, totOut, totCR, totCC int

	cur := newest
	for !cur.Before(oldest) {
		key := cur.Format("2006-01-02")
		d, active := dayMap[key]

		dateStr := ruleStyle.Render(cur.Format("2006-01-02"))
		sb.WriteString("  ")
		sb.WriteString(dateStr)
		sb.WriteString("   ")

		if !active {
			sb.WriteString(dimStyle.Render("·"))
			sb.WriteByte('\n')
		} else {
			tot := tokenTotal(d.Tokens)
			bar := sparkBar(tot, maxTotal)
			// Pad bar to fixed visual width (sparkWidth cells + spaces).
			// lipgloss.Width strips ANSI codes and counts display cells.
			// Guard before Repeat — a future sparkBar change that allows
			// cells > sparkWidth would otherwise panic on negative count.
			barVisualW := lipgloss.Width(bar)
			barPad := ""
			if barVisualW < sparkWidth {
				barPad = strings.Repeat(" ", sparkWidth-barVisualW)
			}
			sb.WriteString(bar)
			sb.WriteString(barPad)
			sb.WriteString("  ")

			inStr := fmt.Sprintf("in %s", formatTokens(d.Tokens.Input))
			outStr := fmt.Sprintf("out %s", formatTokens(d.Tokens.Output))
			crStr := fmt.Sprintf("cache-r %s", formatTokens(d.Tokens.CacheRead))
			ccStr := fmt.Sprintf("cache-c %s", formatTokens(d.Tokens.CacheCreation))
			totStr := fmt.Sprintf("total %s", formatTokens(tot))

			sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", numColW, inStr)))
			sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", numColW, outStr)))
			sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", cacheColW, crStr)))
			sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", cacheColW, ccStr)))
			sb.WriteString(dimStyle.Render(totStr))
			sb.WriteByte('\n')

			totIn += d.Tokens.Input
			totOut += d.Tokens.Output
			totCR += d.Tokens.CacheRead
			totCC += d.Tokens.CacheCreation
		}

		cur = cur.Add(-24 * time.Hour)
	}

	// Footer rule + totals.
	ruleLen := width - 4
	if ruleLen < 1 {
		ruleLen = 1
	}
	sb.WriteString("  ")
	sb.WriteString(ruleStyle.Render(strings.Repeat("─", ruleLen)))
	sb.WriteByte('\n')

	grandTotal := totIn + totOut + totCR + totCC
	sb.WriteString("  ")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-10s", "total")))
	// Active rows lay out as: date(10) + gap(3) + bar(sparkWidth) + post-bar gap(2).
	// barCol == sparkWidth + 2 covers (bar + post-bar gap), so the leading
	// gap of 3 spaces is what we need to mirror after the "total" label.
	sb.WriteString(strings.Repeat(" ", barCol+3))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", numColW, fmt.Sprintf("in %s", formatTokens(totIn)))))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", numColW, fmt.Sprintf("out %s", formatTokens(totOut)))))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", cacheColW, fmt.Sprintf("cache-r %s", formatTokens(totCR)))))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", cacheColW, fmt.Sprintf("cache-c %s", formatTokens(totCC)))))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("total %s", formatTokens(grandTotal))))

	return sb.String()
}

