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
