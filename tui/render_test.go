package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jonasross/canopy/tui"
)

// TestFormatRelativeTime verifies that the relative-time formatter produces
// human-readable strings for various durations. The spec requires that each
// row contain "a relative-time string for Worktree.LastCommit.When".
func TestFormatRelativeTime(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		when    time.Time
		want    string // substring that must appear
		notWant string // substring that must NOT appear (optional)
	}{
		{
			name: "just now",
			when: now.Add(-10 * time.Second),
			want: "now",
		},
		{
			name: "five minutes ago",
			when: now.Add(-5 * time.Minute),
			want: "5m",
		},
		{
			name: "two hours ago",
			when: now.Add(-2 * time.Hour),
			want: "2h",
		},
		{
			name: "one day ago",
			when: now.Add(-25 * time.Hour),
			want: "1d",
		},
		{
			name: "zero time (no commit)",
			when: time.Time{},
			want: "", // must not panic; any output is acceptable
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tui.FormatRelativeTime(tc.when, now)
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("FormatRelativeTime(%v, now) = %q; want substring %q", tc.when, got, tc.want)
			}
			if tc.notWant != "" && strings.Contains(got, tc.notWant) {
				t.Errorf("FormatRelativeTime(%v, now) = %q; must not contain %q", tc.when, got, tc.notWant)
			}
		})
	}
}

// TestFormatAheadBehind verifies the ahead/behind formatter produces correct
// strings. The spec requires ahead/behind only when HasUpstream.
func TestFormatAheadBehind(t *testing.T) {
	tests := []struct {
		name        string
		ahead       int
		behind      int
		hasUpstream bool
		want        string // substring; empty means empty string is acceptable
		mustBeEmpty bool   // result must be ""
	}{
		{
			name:        "no upstream — empty",
			ahead:       5,
			behind:      3,
			hasUpstream: false,
			mustBeEmpty: true,
		},
		{
			name:        "ahead only",
			ahead:       2,
			behind:      0,
			hasUpstream: true,
			want:        "2",
		},
		{
			name:        "behind only",
			ahead:       0,
			behind:      4,
			hasUpstream: true,
			want:        "4",
		},
		{
			name:        "both",
			ahead:       1,
			behind:      1,
			hasUpstream: true,
			want:        "1",
		},
		{
			name:        "up-to-date with upstream",
			ahead:       0,
			behind:      0,
			hasUpstream: true,
			// "0↑ 0↓" is acceptable, as is empty — dev decides. We just
			// verify it does not panic.
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tui.FormatAheadBehind(tc.ahead, tc.behind, tc.hasUpstream)
			if tc.mustBeEmpty && got != "" {
				t.Errorf("FormatAheadBehind(%d, %d, false) = %q; want empty string", tc.ahead, tc.behind, got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("FormatAheadBehind(%d, %d, %v) = %q; want substring %q",
					tc.ahead, tc.behind, tc.hasUpstream, got, tc.want)
			}
		})
	}
}

// TestFormatBranch verifies that FormatBranch returns "(detached)" when
// the worktree has no branch name, and the raw branch string otherwise.
//
// Acceptance criterion: "the branch string (or (detached) when
// Worktree.Detached is true)"
func TestFormatBranch(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		detached bool
		want     string
	}{
		{
			name:     "normal branch",
			branch:   "feat/my-feature",
			detached: false,
			want:     "feat/my-feature",
		},
		{
			name:     "detached head",
			branch:   "",
			detached: true,
			want:     "detached",
		},
		{
			name:     "detached with branch string (ignore branch)",
			branch:   "something",
			detached: true,
			want:     "detached",
		},
		{
			name:     "main branch",
			branch:   "main",
			detached: false,
			want:     "main",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tui.FormatBranch(tc.branch, tc.detached)
			if !strings.Contains(got, tc.want) {
				t.Errorf("FormatBranch(%q, %v) = %q; want substring %q",
					tc.branch, tc.detached, got, tc.want)
			}
		})
	}
}
