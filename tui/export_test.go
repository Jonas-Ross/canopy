package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// Test-only seams. Each function here exposes an unexported symbol so
// tui_test (the external test package) can drive paths that would
// otherwise be unreachable. Production code never calls these.

// Message constructors — exposed so tests can drive Model.Update on the
// result-message switch arms (success and error paths).

func MakeWorktreeRemovedMsg(path string, err error) tea.Msg {
	return worktreeRemovedMsg{path: path, err: err}
}

func MakeWorktreeCreatedMsg(branch, path string, err error) tea.Msg {
	return worktreeCreatedMsg{branch: branch, path: path, err: err}
}

func MakeProcsKilledMsg(count int, err error) tea.Msg {
	return procsKilledMsg{count: count, err: err}
}

func MakePROpenedMsg(err error) tea.Msg {
	return prOpenedMsg{err: err}
}

func MakeShellDroppedMsg(err error) tea.Msg {
	return shellDroppedMsg{err: err}
}

func MakeBlinkTickMsg() tea.Msg {
	return blinkTickMsg{}
}

// Cmd factories — exposed so soft-gate tests can call the cmds directly
// and observe the returned message without going through the Update loop.

func RemoveWorktreeCmdForTest(path string) tea.Cmd { return removeWorktreeCmd(context.Background(), path) }
func KillProcsCmdForTest(pids []int) tea.Cmd       { return killProcsCmd(pids, "") }
func OpenURLCmdForTest(url string) tea.Cmd         { return openURLCmd(url) }

// Ctx-aware variants for tests that need to assert run-ctx propagation
// (e.g. cancellation tests for #23).
func RemoveWorktreeCmdForTestCtx(ctx context.Context, path string) tea.Cmd {
	return removeWorktreeCmd(ctx, path)
}
func CreateWorktreeCmdForTestCtx(ctx context.Context, repoRoot, branch, base string) tea.Cmd {
	return createWorktreeCmd(ctx, repoRoot, branch, base)
}

// CleanExecErrForTest exposes the unexported helper that strips the noisy
// "exit status N:" prefix from wrapped exec errors (#25).
func CleanExecErrForTest(err error, output []byte) error {
	return cleanExecErr(err, output)
}

// WrapKillSignalErrorForTest exposes the unexported helper that annotates a
// SIGTERM failure with its PID (#25).
func WrapKillSignalErrorForTest(pid int, err error) error {
	return wrapKillSignalError(pid, err)
}

// Mode introspection — for asserting a Model is in the right modal state.

const (
	ModeNormalForTest       = int(modeNormal)
	ModeFilteringForTest    = int(modeFiltering)
	ModeConfirmPruneForTest = int(modeConfirmPrune)
	ModeConfirmKillForTest  = int(modeConfirmKill)
	ModeNewWorktreeForTest  = int(modeNewWorktree)
)

// ModeOf reports the current mode of a Model. Returns -1 on type miss.
func ModeOf(m tea.Model) int {
	if mm, ok := m.(Model); ok {
		return int(mm.mode)
	}
	return -1
}

// NoticeOf reports the current notice text of a Model. Returns "" on miss.
func NoticeOf(m tea.Model) string {
	if mm, ok := m.(Model); ok {
		return mm.notice
	}
	return ""
}

// OrderedPaths returns the worktree paths the Model is currently tracking,
// in their order of arrival. Returns nil on a type-assertion miss.
func OrderedPaths(m tea.Model) []string {
	if mm, ok := m.(Model); ok {
		out := make([]string, len(mm.ordered))
		copy(out, mm.ordered)
		return out
	}
	return nil
}

// Pure-helper exports for direct testing.

func ElidePath(path string, max int) string { return elidePath(path, max) }
func Truncate(s string, max int) string     { return truncate(s, max) }

// CreateWorktreeCmdForTest exposes createWorktreeCmd so tests can drive
// the branch-name validation and soft-gate paths.
func CreateWorktreeCmdForTest(repoRoot, branch, base string) tea.Cmd {
	return createWorktreeCmd(context.Background(), repoRoot, branch, base)
}

// ValidBranchOrBaseName exposes the unexported validator for direct table
// testing.
func ValidBranchOrBaseName(s string) bool { return validBranchOrBaseName(s) }

// ProcsExpandedFor reports whether the procs section is expanded for the
// given worktree path on this Model. Returns false on a type-assertion miss.
func ProcsExpandedFor(m tea.Model, path string) bool {
	if mm, ok := m.(Model); ok {
		return mm.procsExpanded[path]
	}
	return false
}

// NewFormFocusOf reports the new-worktree form's focused input index
// (0 = branch, 1 = base). Returns -1 on a type-assertion miss.
func NewFormFocusOf(m tea.Model) int {
	if mm, ok := m.(Model); ok {
		return mm.newFormFocus
	}
	return -1
}

// NewFormBranchValueOf returns the current value of the new-worktree
// form's branch input. Returns "" on a type-assertion miss.
func NewFormBranchValueOf(m tea.Model) string {
	if mm, ok := m.(Model); ok {
		return mm.newBranchInput.Value()
	}
	return ""
}

// NewFormBaseValueOf returns the current value of the new-worktree
// form's base input. Returns "" on a type-assertion miss.
func NewFormBaseValueOf(m tea.Model) string {
	if mm, ok := m.(Model); ok {
		return mm.newBaseInput.Value()
	}
	return ""
}

// Tab seams — expose the unexported tab type and constants so tui_test can
// drive and assert the active tab without reaching into unexported fields.

// Tab is the exported alias for the unexported tab type.
type Tab = tab

const (
	TabOperational = tabOperational
	TabForensics   = tabForensics
)

// ActiveTab returns the active tab of a Model. Returns TabOperational on a
// type-assertion miss.
func ActiveTab(m tea.Model) Tab {
	if mm, ok := m.(Model); ok {
		return mm.tab
	}
	return TabOperational
}

// BlinkPhaseOf returns the current phase of the live-indicator blink:
// true = on (bright), false = off (dim). Returns false on a type-assertion
// miss.
func BlinkPhaseOf(m tea.Model) bool {
	if mm, ok := m.(Model); ok {
		return mm.blinkPhase
	}
	return false
}

// BlinkRunningOf reports whether a blink tick is currently in flight.
// Returns false on a type-assertion miss.
func BlinkRunningOf(m tea.Model) bool {
	if mm, ok := m.(Model); ok {
		return mm.blinkRunning
	}
	return false
}

// SetBlinkPhaseForTest forces the model's blinkPhase to the given value,
// so goldens can pin either phase deterministically without driving the
// tick lifecycle.
func SetBlinkPhaseForTest(m tea.Model, phase bool) tea.Model {
	if mm, ok := m.(Model); ok {
		mm.blinkPhase = phase
		return mm
	}
	return m
}
