package demo

import "github.com/jonasross/canopy/procs"

// HeavyProcs returns a stable 14-entry process list rooted at cwd, mixing
// claude, build tools, and editor/MCP noise — enough to exercise the TUI's
// proc-panel cap-and-expand affordance. Pid ordering is intentional: higher
// pid wins within each tier, so the ranker has multiple ties to break.
func HeavyProcs(cwd string) []procs.Process {
	return []procs.Process{
		{Pid: 11201, Cwd: cwd, Command: "claude", Args: []string{"claude"}},
		{Pid: 11200, Cwd: cwd, Command: "claude", Args: []string{"claude"}},
		{Pid: 11150, Cwd: cwd, Command: "go", Args: []string{"go", "test", "./..."}},
		{Pid: 11140, Cwd: cwd, Command: "npm", Args: []string{"npm", "run", "dev"}},
		{Pid: 11100, Cwd: cwd, Command: "zsh", Args: []string{"-zsh"}},
		{Pid: 11101, Cwd: cwd, Command: "zsh", Args: []string{"-zsh"}},
		{Pid: 11102, Cwd: cwd, Command: "zsh", Args: []string{"-zsh"}},
		{Pid: 11050, Cwd: cwd, Command: "gopls", Args: []string{"gopls", "-mode=stdio"}},
		{Pid: 11020, Cwd: cwd, Command: "node", Args: []string{"node", "/opt/mcp/context7"}},
		{Pid: 11021, Cwd: cwd, Command: "node", Args: []string{"node", "/opt/mcp/semble"}},
		{Pid: 11030, Cwd: cwd, Command: "python", Args: []string{"python", "/opt/mcp/wiki"}},
		{Pid: 10900, Cwd: cwd, Command: "uv", Args: []string{"uv", "run", "server"}},
		{Pid: 10850, Cwd: cwd, Command: "Cursor Helper", Args: []string{"Cursor Helper (Plugin)"}},
		{Pid: 10860, Cwd: cwd, Command: "Cursor Helper", Args: []string{"Cursor Helper (Plugin)"}},
	}
}
