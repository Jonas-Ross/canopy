package analytics

import (
	"time"

	"github.com/jonasross/canopy/sessions"
)

// DayBucket is the per-day token totals for the spend view, optionally
// filtered to one model. Date is in UTC at midnight.
type DayBucket struct {
	Date   time.Time
	Model  string // "" = all models combined
	Tokens sessions.TokenStats
	// SessionCount is the number of distinct sessions contributing to
	// this day. A session whose lifetime spans midnight contributes to
	// each day it touched.
	SessionCount int
}

// SessionSummary is the row shape for the per-session view. Mirrors the
// information already on sessions.Session, surfaced in display order.
type SessionSummary struct {
	ID          string
	Model       string
	Worktree    string // the deepest cwd that matched a worktree, or last Cwds entry
	StartedAt   time.Time
	UpdatedAt   time.Time
	Duration    time.Duration
	Prompts     int    // count of user lines
	ToolCalls   int    // sum over Tools map
	Tokens      sessions.TokenStats
	IsSidechain bool
}

// ToolUsage is one row of the tool-distribution view. Bucketed by
// (Model, Tool); Count is the total invocations across the window.
type ToolUsage struct {
	Model string
	Tool  string
	Count int
}

// WorktreeSummary is the row shape for the per-worktree view. Path is
// the worktree filesystem path; LastSeen is the max UpdatedAt across
// its sessions. CwdPrefix-correlated, not git-correlated.
type WorktreeSummary struct {
	Path         string
	SessionCount int
	TotalTime    time.Duration
	LastSeen     time.Time
}

// Snapshot bundles the four view inputs for a single render. Built by
// Build(); held on the TUI Model. Cheap to construct (forensics-tab is
// not in the hot refresh path) so we recompute rather than incrementally
// patch on each aggregator update.
type Snapshot struct {
	GeneratedAt time.Time
	WindowStart time.Time // earliest day considered (now - 30 days, by default)
	WindowEnd   time.Time

	Days      []DayBucket       // sorted DESC by Date for render
	Sessions  []SessionSummary  // sorted DESC by UpdatedAt, capped at recentSessionsLimit
	Tools     []ToolUsage       // sorted by (Model asc, Count desc)
	Worktrees []WorktreeSummary // sorted DESC by TotalTime

	// SessionCountByModel is the count of sessions in [WindowStart,
	// WindowEnd] keyed by Model. Distinct from len(Sessions) because
	// the Sessions slice is capped — this count is uncapped and is
	// what the tools view should display in its per-model header.
	SessionCountByModel map[string]int
}
