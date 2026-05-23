package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/internal/ansi"
)

func TestCategorizeTool(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTag   string
		wantStyle lipgloss.Style
	}{
		// file
		{"Read", "Read", "file", toolTagFileStyle},
		{"Write", "Write", "file", toolTagFileStyle},
		{"Edit", "Edit", "file", toolTagFileStyle},
		{"MultiEdit", "MultiEdit", "file", toolTagFileStyle},
		{"NotebookEdit", "NotebookEdit", "file", toolTagFileStyle},
		{"Grep", "Grep", "file", toolTagFileStyle},
		{"Glob", "Glob", "file", toolTagFileStyle},
		// exec
		{"Bash", "Bash", "exec", toolTagExecStyle},
		{"KillShell", "KillShell", "exec", toolTagExecStyle},
		{"BashOutput", "BashOutput", "exec", toolTagExecStyle},
		{"Monitor", "Monitor", "exec", toolTagExecStyle},
		// web
		{"WebFetch", "WebFetch", "web", toolTagWebStyle},
		{"WebSearch", "WebSearch", "web", toolTagWebStyle},
		// mcp (prefix match — any name starting with mcp__)
		{"mcp simple", "mcp__semble__search", "mcp", toolTagMCPStyle},
		{"mcp long", "mcp__plugin_github_github__create_pull_request", "mcp", toolTagMCPStyle},
		// task
		{"Task", "Task", "task", toolTagTaskStyle},
		{"TaskCreate", "TaskCreate", "task", toolTagTaskStyle},
		{"TaskUpdate", "TaskUpdate", "task", toolTagTaskStyle},
		{"TaskList", "TaskList", "task", toolTagTaskStyle},
		{"TaskGet", "TaskGet", "task", toolTagTaskStyle},
		{"TaskOutput", "TaskOutput", "task", toolTagTaskStyle},
		{"TaskStop", "TaskStop", "task", toolTagTaskStyle},
		{"SendMessage", "SendMessage", "task", toolTagTaskStyle},
		{"Skill", "Skill", "task", toolTagTaskStyle},
		{"ToolSearch", "ToolSearch", "task", toolTagTaskStyle},
		// other / default
		{"unknown", "SomethingNew", "·", toolTagDimStyle},
		{"empty string", "", "·", toolTagDimStyle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTag, gotStyle := categorizeTool(tt.input)
			if gotTag != tt.wantTag {
				t.Errorf("categorizeTool(%q) tag = %q, want %q", tt.input, gotTag, tt.wantTag)
			}
			// Styles compare by their rendered output for a fixed input —
			// lipgloss.Style isn't directly comparable, but rendering the
			// same string with the same color produces identical output.
			gotRender := gotStyle.Render("x")
			wantRender := tt.wantStyle.Render("x")
			if gotRender != wantRender {
				t.Errorf("categorizeTool(%q) style render = %q, want %q", tt.input, gotRender, wantRender)
			}
		})
	}
}

func TestFormatToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxWidth int
		want     string
	}{
		// Non-MCP names pass through.
		{"Read passthrough", "Read", 20, "Read"},
		{"Bash passthrough", "Bash", 20, "Bash"},
		{"NotebookEdit passthrough", "NotebookEdit", 20, "NotebookEdit"},
		{"empty passthrough", "", 20, ""},

		// maxWidth <= 0 disables truncation per docstring.
		{"maxWidth zero disables truncation", "mcp__plugin_github_github__create_pull_request", 0, "github/create_pull_request"},

		// MCP: simple two-segment server, fits.
		{"mcp simple", "mcp__semble__search", 20, "semble/search"},
		{"mcp wiki", "mcp__wiki__write_page", 20, "wiki/write_page"},
		{"mcp ide", "mcp__ide__getDiagnostics", 20, "ide/getDiagnostics"},

		// MCP: multi-segment server collapses to its last segment.
		{"mcp plugin github short", "mcp__plugin_github_github__list_issues", 20, "github/list_issues"},
		{"mcp plugin context7", "mcp__plugin_context7_context7__resolve-library-id", 30, "context7/resolve-library-id"},

		// MCP: result longer than maxWidth — middle-truncate the action.
		{"mcp action truncated", "mcp__plugin_github_github__create_pull_request", 20, "github/create_pu…est"},

		// MCP: server alone equals or exceeds maxWidth — action becomes "…".
		{"mcp server too long", "mcp__superlongservername__do_thing", 12, "superlongservername/…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolName(tt.input, tt.maxWidth)
			if got != tt.want {
				t.Errorf("formatToolName(%q, %d) = %q, want %q", tt.input, tt.maxWidth, got, tt.want)
			}
		})
	}
}

func TestProportionalBar_cellCounts(t *testing.T) {
	const width = 20
	tests := []struct {
		name      string
		count     int
		total     int
		wantFull  int // count of full block "█" cells
		wantTrack int // count of dim track "░" cells
		wantPart  bool
	}{
		{"zero count zero total", 0, 0, 0, width, false},
		{"zero count non-zero total", 0, 1000, 0, width, false},
		{"100 percent", 100, 100, width, 0, false},
		{"50 percent exact", 50, 100, 10, 10, false},
		{"47 percent partial", 47, 100, 9, 10, true},
		{"5 percent exact one cell", 5, 100, 1, 19, false},
		{"1 percent partial", 1, 100, 0, 19, true},
		// Tiny ratio still produces a visible partial glyph (the smallest
		// from horizontalBlocks). The raw.Len() == 0 floor is unreachable
		// for these integer inputs but is kept as a safety net.
		{"tiny ratio shows smallest partial glyph", 1, 10_000, 0, 19, true},
		// count > totalCalls shouldn't happen but we guarantee the fill+track
		// width invariant regardless — fill clamps to cellWidth, no overflow.
		{"count exceeds total", 150, 100, width, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fill, track := proportionalBar(tt.count, tt.total, width)
			plainFill := ansi.Strip(fill)
			plainTrack := ansi.Strip(track)
			gotFull := strings.Count(plainFill, "█")
			gotTrack := strings.Count(plainTrack, "░")
			gotPart := false
			for _, r := range plainFill {
				if r != '█' {
					gotPart = true
					break
				}
			}
			if gotFull != tt.wantFull {
				t.Errorf("full cells = %d, want %d (fill=%q)", gotFull, tt.wantFull, plainFill)
			}
			if gotTrack != tt.wantTrack {
				t.Errorf("track cells = %d, want %d (track=%q)", gotTrack, tt.wantTrack, plainTrack)
			}
			if gotPart != tt.wantPart {
				t.Errorf("has partial = %v, want %v (fill=%q)", gotPart, tt.wantPart, plainFill)
			}
			// Total visual cells (fill + track) must always equal width.
			total := runeCount(plainFill) + runeCount(plainTrack)
			if total != width {
				t.Errorf("total visual cells = %d, want %d (fill=%q track=%q)",
					total, width, plainFill, plainTrack)
			}
		})
	}
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
