package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/tui"
)

// Tier-3 pure-helper tests — these formatters / sizers are exercised
// transitively by goldens, but isolated tests pin their semantics and
// fail with a clearer signal when something drifts.

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short stays put", "hello", 10, "hello"},
		{"exact fits", "hello", 5, "hello"},
		{"too long gets ellipsed", "hellothere", 5, "hell…"},
		{"unicode counted by runes", "héllo", 5, "héllo"},
		{"unicode truncated by runes", "héllotoolong", 5, "héll…"},
		{"single char max", "abcdef", 1, "…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.Truncate(tc.in, tc.max); got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestElidePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string // suffix-check; HOME substitution is environment-dependent
	}{
		{"short stays put", "/foo/bar", 20, "/foo/bar"},
		{"long tail-elided", "/very/long/absolute/path/to/something", 10, "…something"},
		{"exact fits", "/abc/def", 8, "/abc/def"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tui.ElidePath(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("ElidePath(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestElidePath_HomeSubstitution(t *testing.T) {
	// HOME is set in nearly every test environment; if not, skip rather
	// than make the test environment-fragile.
	t.Setenv("HOME", "/home/jonas")
	got := tui.ElidePath("/home/jonas/projects/canopy/.worktrees/feat", 100)
	if !strings.HasPrefix(got, "~") {
		t.Errorf("ElidePath did not substitute $HOME with '~': got %q", got)
	}
}

func TestView_WidthClampedToMinimum(t *testing.T) {
	// View() falls back to width=80 when the supplied width is <=0.
	// Confirm the render doesn't panic and produces non-empty output.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "main")},
	}))

	view := stripANSI(m.View())
	if !strings.Contains(view, "main") {
		t.Errorf("zero-width view missing branch; got:\n%s", view)
	}
}

func TestView_NegativeWidthClampedToMinimum(t *testing.T) {
	// Pathological negative width — the clamp must apply and rendering
	// must not panic.
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: -10, Height: -5})
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{
		Worktree: "/repo/wt-a",
		State:    aggregator.WorktreeState{Worktree: newBaseWorktree("/repo/wt-a", "main")},
	}))

	// As long as View() returns without panic, the clamp is doing its job.
	if view := m.View(); view == "" {
		t.Errorf("negative-width view returned empty string")
	}
}
