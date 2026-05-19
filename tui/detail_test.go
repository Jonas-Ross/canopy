package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jonasross/canopy/internal/ansi"
	"github.com/jonasross/canopy/procs"
)

func makeProcs(n int, cmd string) []procs.Process {
	out := make([]procs.Process, n)
	for i := 0; i < n; i++ {
		out[i] = procs.Process{Pid: 1000 + i, Command: cmd}
	}
	return out
}

// procRowRE matches rendered proc rows: leading pid token followed by command.
// The pid is the first whitespace-separated token; "+N more" rows start with
// "+" and don't match.
var procRowRE = regexp.MustCompile(`^\s*\d+\s+\S`)

func countProcLines(rendered string) int {
	n := 0
	for _, line := range strings.Split(rendered, "\n") {
		if procRowRE.MatchString(ansi.Strip(line)) {
			n++
		}
	}
	return n
}

func TestRenderDetailProcs_Empty(t *testing.T) {
	if got := renderDetailProcs(nil, -1, false); got != "" {
		t.Errorf("empty list should render nothing, got %q", got)
	}
	if got := renderDetailProcs([]procs.Process{}, -1, false); got != "" {
		t.Errorf("empty list should render nothing, got %q", got)
	}
}

func TestRenderDetailProcs_NoCapNoMore(t *testing.T) {
	got := renderDetailProcs(makeProcs(3, "claude"), -1, false)
	if n := countProcLines(got); n != 3 {
		t.Errorf("collapsed, 3 procs, unbounded budget: want 3 rows, got %d\n%s", n, got)
	}
	if strings.Contains(got, "more") {
		t.Errorf("no overflow, should not contain 'more':\n%s", got)
	}
}

func TestRenderDetailProcs_CollapsedSoftCap(t *testing.T) {
	got := renderDetailProcs(makeProcs(10, "zsh"), -1, false)
	if n := countProcLines(got); n != 5 {
		t.Errorf("collapsed, 10 procs, unbounded: want 5 rows, got %d\n%s", n, got)
	}
	if !strings.Contains(got, "+5 more") {
		t.Errorf("want '+5 more' in:\n%s", got)
	}
	if !strings.Contains(got, "(P)") {
		t.Errorf("want '(P)' hint in:\n%s", got)
	}
}

func TestRenderDetailProcs_ExpandedShowsAll(t *testing.T) {
	got := renderDetailProcs(makeProcs(10, "zsh"), -1, true)
	if n := countProcLines(got); n != 10 {
		t.Errorf("expanded, 10 procs, unbounded: want 10 rows, got %d\n%s", n, got)
	}
	if strings.Contains(got, "more") {
		t.Errorf("expanded should not contain 'more':\n%s", got)
	}
}

func TestRenderDetailProcs_BudgetClampsBelowSoftCap(t *testing.T) {
	// budget=4 → header + 2 procs + "+N more" = 4 lines.
	got := renderDetailProcs(makeProcs(10, "zsh"), 4, false)
	if n := countProcLines(got); n != 2 {
		t.Errorf("budget=4, 10 procs: want 2 rows, got %d\n%s", n, got)
	}
	if !strings.Contains(got, "+8 more") {
		t.Errorf("want '+8 more' in:\n%s", got)
	}
}

func TestRenderDetailProcs_ExpandedHonorsBudget(t *testing.T) {
	got := renderDetailProcs(makeProcs(10, "zsh"), 4, true)
	if n := countProcLines(got); n != 2 {
		t.Errorf("budget=4, expanded, 10 procs: want 2 rows, got %d\n%s", n, got)
	}
	if !strings.Contains(got, "+8 more") {
		t.Errorf("want '+8 more' in:\n%s", got)
	}
}

func TestRenderDetailProcs_RankingPlacesClaudeFirst(t *testing.T) {
	list := []procs.Process{
		{Pid: 100, Command: "zsh"},
		{Pid: 200, Command: "zsh"},
		{Pid: 300, Command: "claude"},
		{Pid: 400, Command: "zsh"},
		{Pid: 500, Command: "zsh"},
		{Pid: 600, Command: "zsh"},
		{Pid: 700, Command: "zsh"},
	}
	got := renderDetailProcs(list, -1, false)
	if !strings.Contains(got, "300") {
		t.Errorf("claude pid 300 should appear when collapsed:\n%s", got)
	}
}

func TestRenderDetailProcs_BudgetEqualToShowsNoMore(t *testing.T) {
	// budget=6 = 1 header + 5 procs, no overflow needed.
	got := renderDetailProcs(makeProcs(5, "claude"), 6, false)
	if n := countProcLines(got); n != 5 {
		t.Errorf("budget=6, 5 procs: want 5 rows, got %d\n%s", n, got)
	}
	if strings.Contains(got, "more") {
		t.Errorf("no overflow when len ≤ cap and budget allows:\n%s", got)
	}
}
