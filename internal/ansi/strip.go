// Package ansi provides ANSI escape helpers shared by the validation loop
// (canopy demo script captures and tui golden frames). Production rendering
// paths don't import this; production code never strips ANSI from its own
// output.
package ansi

import "regexp"

// sgr matches CSI SGR (color/style) escape sequences. Sufficient for
// lipgloss output, which only emits foreground/background/style codes.
var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Strip removes all SGR escapes from s.
func Strip(s string) string { return sgr.ReplaceAllString(s, "") }
