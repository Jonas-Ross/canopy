// Package tui implements the bubbletea TUI operational view for Canopy.
// This file is a stub that satisfies the acceptance test compilation surface.
// Dev must replace these stubs with real implementations.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
)

// Refresher is the interface the Model uses to trigger a data refresh.
// Production code passes *aggregator.Aggregator; tests inject a fake.
type Refresher interface {
	Refresh()
}

// UpdateMsg wraps an aggregator.Update so it can be sent as a bubbletea
// message via tea.Program.Send. The bridge goroutine in Run produces these.
type UpdateMsg aggregator.Update

// Model is the root bubbletea model for the Canopy TUI.
type Model struct {
	// TODO: Dev implements real fields.
	refresher Refresher
}

// NewModel constructs the root Model with the given Refresher. Tests call
// this directly; Run constructs it internally.
func NewModel(r Refresher) tea.Model {
	return Model{refresher: r}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model. All I/O (bridge goroutine, signal handlers)
// lives outside this function to keep it pure and unit-testable.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// TODO: Dev implements real state transitions.
	_ = msg
	return m, nil
}

// View implements tea.Model.
func (m Model) View() string {
	// TODO: Dev implements real rendering.
	return ""
}

// Run constructs the bubbletea program, wires the aggregator subscription
// bridge goroutine, and blocks until the program exits or ctx is cancelled.
// On exit, ctx is cancelled and agg.Close() is called.
func Run(ctx context.Context, agg *aggregator.Aggregator) error {
	// TODO: Dev implements real entrypoint.
	_ = ctx
	_ = agg
	return nil
}

// FocusIndex returns the currently focused row index. Exported for tests.
func FocusIndex(m tea.Model) int {
	// TODO: Dev implements.
	return 0
}

// IsFiltering reports whether the model is in filter-input mode. Exported for tests.
func IsFiltering(m tea.Model) bool {
	// TODO: Dev implements.
	return false
}

// FilterValue returns the current filter string. Exported for tests.
func FilterValue(m tea.Model) string {
	// TODO: Dev implements.
	return ""
}

// FormatRelativeTime returns a short human-readable relative time string for
// when, relative to now. Used by the worktree list renderer.
// Exported for table-driven unit tests.
func FormatRelativeTime(when, now time.Time) string {
	// TODO: Dev implements.
	return ""
}

// FormatAheadBehind returns the ahead/behind indicator string. Returns ""
// when hasUpstream is false. Exported for table-driven unit tests.
func FormatAheadBehind(ahead, behind int, hasUpstream bool) string {
	// TODO: Dev implements.
	return ""
}

// FormatBranch returns the display string for a branch: the branch name, or
// "(detached)" when detached is true. Exported for table-driven unit tests.
func FormatBranch(branch string, detached bool) string {
	// TODO: Dev implements.
	return ""
}
