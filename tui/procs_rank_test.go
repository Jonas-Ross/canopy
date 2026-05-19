package tui

import (
	"testing"

	"github.com/jonasross/canopy/procs"
)

func TestIsBuildToolProc(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		{"cargo", "cargo", []string{"cargo", "build"}, true},
		{"make", "make", []string{"make", "test"}, true},
		{"pytest", "pytest", []string{"pytest", "-x"}, true},
		{"npm", "npm", []string{"npm", "run", "dev"}, true},
		{"pnpm", "pnpm", []string{"pnpm", "install"}, true},
		{"bun", "bun", []string{"bun", "run", "build"}, true},
		{"go test", "go", []string{"go", "test", "./..."}, true},
		{"go build", "go", []string{"go", "build", "./..."}, true},
		{"go run", "go", []string{"go", "run", "main.go"}, true},
		{"bare go (not a build tool)", "go", []string{"go"}, false},
		{"gopls (not a build tool)", "gopls", []string{"gopls", "-mode=stdio"}, false},
		{"go vet (not in starter set)", "go", []string{"go", "vet", "./..."}, false},
		{"zsh shell", "zsh", []string{"-zsh"}, false},
		{"node MCP server", "node", []string{"node", "/path/to/mcp"}, false},
		{"empty", "", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBuildToolProc(tc.cmd, tc.args); got != tc.want {
				t.Errorf("isBuildToolProc(%q, %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}

func TestRankProcs_TierOrdering(t *testing.T) {
	in := []procs.Process{
		{Pid: 100, Command: "zsh", Args: []string{"-zsh"}},
		{Pid: 200, Command: "claude", Args: []string{"claude"}},
		{Pid: 300, Command: "make", Args: []string{"make"}},
		{Pid: 400, Command: "gopls", Args: []string{"gopls"}},
		{Pid: 500, Command: "go", Args: []string{"go", "test", "./..."}},
		{Pid: 600, Command: "node", Args: []string{"node", "/path/claude-code"}},
	}
	got := rankProcs(in)

	// Tier 1 (claude): pids 200 and 600, ordered pid descending → 600, 200.
	// Tier 2 (build tools): pids 300, 500, ordered pid descending → 500, 300.
	// Tier 3 (everything else): pids 100, 400, ordered pid descending → 400, 100.
	wantPids := []int{600, 200, 500, 300, 400, 100}
	if len(got) != len(wantPids) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(wantPids), got)
	}
	for i, p := range got {
		if p.Pid != wantPids[i] {
			t.Errorf("got[%d].Pid = %d, want %d", i, p.Pid, wantPids[i])
		}
	}
}

func TestRankProcs_PidDescendingWithinTier(t *testing.T) {
	in := []procs.Process{
		{Pid: 50, Command: "claude"},
		{Pid: 80, Command: "claude"},
		{Pid: 30, Command: "claude"},
	}
	got := rankProcs(in)
	wantPids := []int{80, 50, 30}
	for i, p := range got {
		if p.Pid != wantPids[i] {
			t.Errorf("got[%d].Pid = %d, want %d", i, p.Pid, wantPids[i])
		}
	}
}

func TestRankProcs_EmptyAndNil(t *testing.T) {
	if got := rankProcs(nil); len(got) != 0 {
		t.Errorf("rankProcs(nil) = %v, want empty", got)
	}
	if got := rankProcs([]procs.Process{}); len(got) != 0 {
		t.Errorf("rankProcs(empty) = %v, want empty", got)
	}
}

func TestRankProcs_DoesNotMutateInput(t *testing.T) {
	in := []procs.Process{
		{Pid: 1, Command: "zsh"},
		{Pid: 2, Command: "claude"},
	}
	orig := append([]procs.Process(nil), in...)
	_ = rankProcs(in)
	for i, p := range in {
		if p.Pid != orig[i].Pid {
			t.Errorf("input mutated at %d: got pid %d, want %d", i, p.Pid, orig[i].Pid)
		}
	}
}
