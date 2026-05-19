package tui

import (
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

// Cmd factories — exposed so soft-gate tests can call the cmds directly
// and observe the returned message without going through the Update loop.

func RemoveWorktreeCmdForTest(path string) tea.Cmd { return removeWorktreeCmd(path) }
func KillProcsCmdForTest(pids []int) tea.Cmd       { return killProcsCmd(pids, "") }
func OpenURLCmdForTest(url string) tea.Cmd         { return openURLCmd(url) }

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
	return createWorktreeCmd(repoRoot, branch, base)
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
