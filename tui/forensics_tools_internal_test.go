package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
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
		{"mcp server too long", "mcp__plugin_verylongservername_x__do_thing", 12, "verylongservername_x/…"},
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
