package aggregator

import (
	"context"
	"time"

	"github.com/jonasross/canopy/git"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/sessions"
)

// LiveWindow is how recently a session must have updated to count as
// "live" for a worktree.
const LiveWindow = 120 * time.Second

// PollInterval is the cadence of the background refresh ticker.
const PollInterval = 30 * time.Second

// RecentSessionsLimit caps the size of WorktreeState.Recent.
const RecentSessionsLimit = 5

// subscriberBufferSize bounds the per-subscriber channel. Slow
// subscribers drop events with a counter; they never block the
// state-owner goroutine.
const subscriberBufferSize = 64

// Repo identifies one git repository for the aggregator to track.
type Repo struct {
	Root string
	Name string
}

// WorktreeState is the joined per-worktree view that the TUI consumes.
type WorktreeState struct {
	Repo      Repo
	Worktree  git.Worktree
	PR        *pr.PR
	PRStale   bool
	Procs     []procs.Process
	Live      *sessions.Session
	Recent    []*sessions.Session
	UpdatedAt time.Time
}

// Update is the diff event Subscribe emits.
type Update struct {
	Worktree string
	State    WorktreeState
}

// Stats reports observable counters for the aggregator.
type Stats struct {
	SubscriberDrops uint64
}

// Config wires the aggregator to its inputs. All fields except Repos
// and SessionStore are optional.
type Config struct {
	Repos        []Repo
	SessionStore *sessions.Store
	PRCache      *pr.Cache

	// Test seams. Production code leaves these nil and the aggregator
	// falls back to the real package functions.
	listWorktrees  func(ctx context.Context, repoRoot string) ([]git.Worktree, error)
	worktreeStatus func(ctx context.Context, path string) (git.Worktree, error)
	listProcs      func(ctx context.Context, prefix string) ([]procs.Process, error)
	now            func() time.Time
}
