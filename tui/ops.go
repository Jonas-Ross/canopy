package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
)

type mode int

const (
	modeNormal mode = iota
	modeFiltering
	modeConfirmPrune
	modeConfirmKill
	modeNewWorktree
)

type prOpenedMsg struct {
	err error
}

type shellDroppedMsg struct {
	err error
}

type worktreeRemovedMsg struct {
	path string
	err  error
}

type worktreeCreatedMsg struct {
	branch string
	path   string
	err    error
}

type procsKilledMsg struct {
	count   int
	skipped int // procs skipped because /proc/<pid>/cwd no longer matches
	err     error
}

type pulseExpiredMsg struct{}

// isDemoMode reports whether the binary is running under `canopy demo`. The
// flag short-circuits any operation that would otherwise touch the user's
// real environment (browser, processes, worktree tree) so an automated
// validation script can't escape the sandbox even by accident.
func isDemoMode() bool {
	return os.Getenv("CANOPY_DEMO") == "1"
}

func (m Model) focusedState() (aggregator.WorktreeState, bool) {
	if m.focusIndex < 0 || m.focusIndex >= len(m.ordered) {
		return aggregator.WorktreeState{}, false
	}
	state, ok := m.states[m.ordered[m.focusIndex]]
	if !ok {
		return aggregator.WorktreeState{}, false
	}
	// A worktree filtered out of the list cannot be the action target.
	// Without this guard, d/p/K silently act on a row the user can't see
	// (the cursor disappears when the filter hides the focused row).
	if filter := m.activeFilter(); filter != "" {
		branch := FormatBranch(state.Worktree.Branch, state.Worktree.Detached)
		if !strings.Contains(strings.ToLower(branch), strings.ToLower(filter)) {
			return aggregator.WorktreeState{}, false
		}
	}
	return state, true
}

func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		if isDemoMode() {
			return prOpenedMsg{}
		}
		var c *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			c = exec.Command("open", url)
		case "windows":
			c = exec.Command("cmd", "/c", "start", "", url)
		default:
			c = exec.Command("xdg-open", url)
		}
		// xdg-open's exit codes (3 "no method available", 4 "action failed")
		// only tell you it broke; the helpful message lives on stderr.
		return prOpenedMsg{err: runCapturingStderr(c)}
	}
}

func (m Model) handleOpenPR() (Model, tea.Cmd) {
	state, ok := m.focusedState()
	if !ok {
		m.notice = noticeStyle.Render("no worktree focused")
		return m, nil
	}
	if state.PR == nil || state.PR.URL == "" {
		m.notice = noticeStyle.Render("no PR for " + FormatBranch(state.Worktree.Branch, state.Worktree.Detached))
		return m, nil
	}
	m.notice = noticeStyle.Render("opening " + state.PR.URL)
	return m, openURLCmd(state.PR.URL)
}

func (m Model) handleShellDrop() (Model, tea.Cmd) {
	state, ok := m.focusedState()
	if !ok {
		return m, nil
	}
	if isDemoMode() {
		m.notice = noticeStyle.Render("demo: would open shell tab at " + state.Worktree.Path)
		return m, nil
	}
	return m, openShellTabCmd(state.Worktree.Path)
}

// openShellTabCmd's shellDroppedMsg carries the *spawn* result — it does
// not represent the shell's eventual exit (which the old tea.ExecProcess
// path did).
func openShellTabCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		return shellDroppedMsg{err: openShellTab(dir)}
	}
}

func (m Model) startPrune() (Model, tea.Cmd) {
	state, ok := m.focusedState()
	if !ok {
		return m, nil
	}
	// git refuses `worktree remove` on the primary worktree; surface a notice instead of a no-op prompt.
	if state.Worktree.Main {
		m.notice = noticeStyle.Render("cannot prune primary worktree")
		return m, nil
	}
	m.mode = modeConfirmPrune
	return m, nil
}

func removeWorktreeCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if isDemoMode() {
			return worktreeRemovedMsg{path: path}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", "--", path)
		out, err := c.CombinedOutput()
		if err != nil {
			return worktreeRemovedMsg{path: path, err: fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))}
		}
		return worktreeRemovedMsg{path: path}
	}
}

func (m Model) startKill() (Model, tea.Cmd) {
	state, ok := m.focusedState()
	if !ok || len(state.Procs) == 0 {
		m.notice = noticeStyle.Render("no processes to kill")
		return m, nil
	}
	m.mode = modeConfirmKill
	return m, nil
}

func killProcsCmd(pids []int, expectedCwd string) tea.Cmd {
	return func() tea.Msg {
		if isDemoMode() {
			return procsKilledMsg{count: len(pids)}
		}
		killed, skipped := 0, 0
		var firstErr error
		for _, pid := range pids {
			// PID reuse defense: re-read /proc/<pid>/cwd and confirm it
			// still path-prefix-matches the worktree we listed it under.
			// If the original process died and a new one got the same
			// PID, almost certainly its cwd no longer matches and we
			// skip rather than signal an unrelated process.
			if !pidCwdMatches(pid, expectedCwd) {
				skipped++
				continue
			}
			p, err := os.FindProcess(pid)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := p.Signal(syscall.SIGTERM); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			killed++
		}
		return procsKilledMsg{count: killed, skipped: skipped, err: firstErr}
	}
}

func (m Model) startNewWorktree() (Model, tea.Cmd) {
	branchIn := textinput.New()
	branchIn.Prompt = "branch: "
	branchIn.Placeholder = "feat/new-thing"
	branchIn.Focus()

	baseIn := textinput.New()
	baseIn.Prompt = "base:   "
	baseIn.SetValue("main")

	m.mode = modeNewWorktree
	m.newBranchInput = branchIn
	m.newBaseInput = baseIn
	m.newFormFocus = 0
	return m, textinput.Blink
}

func (m Model) updateNewWorktreeForm(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.newBranchInput.Blur()
		m.newBaseInput.Blur()
		return m, nil

	case tea.KeyTab, tea.KeyShiftTab:
		if m.newFormFocus == 0 {
			m.newBranchInput.Blur()
			m.newBaseInput.Focus()
			m.newFormFocus = 1
		} else {
			m.newBaseInput.Blur()
			m.newBranchInput.Focus()
			m.newFormFocus = 0
		}
		return m, nil

	case tea.KeyEnter:
		branch := strings.TrimSpace(m.newBranchInput.Value())
		base := strings.TrimSpace(m.newBaseInput.Value())
		if branch == "" {
			m.notice = errorStyle.Render("branch name required")
			return m, nil
		}
		if base == "" {
			base = "main"
		}
		m.mode = modeNormal
		m.newBranchInput.Blur()
		m.newBaseInput.Blur()
		return m, createWorktreeCmd(m.repoRoot, branch, base)
	}

	var cmd tea.Cmd
	if m.newFormFocus == 0 {
		m.newBranchInput, cmd = m.newBranchInput.Update(msg)
	} else {
		m.newBaseInput, cmd = m.newBaseInput.Update(msg)
	}
	return m, cmd
}

func worktreeBaseDir(repoRoot string) string {
	for _, d := range []string{".worktrees", "worktrees"} {
		if info, err := os.Stat(filepath.Join(repoRoot, d)); err == nil && info.IsDir() {
			return filepath.Join(repoRoot, d)
		}
	}
	return filepath.Join(repoRoot, ".worktrees")
}

// WorktreePath returns the canonical filesystem path for a worktree of the
// given branch under repoRoot. Slashes in the branch name are replaced with
// "+" because a literal "/" would nest unexpectedly under
// `git worktree list`. Exported so the validation fixture (internal/demo)
// and any future tooling stay in lockstep with where createWorktreeCmd
// actually places worktrees.
func WorktreePath(repoRoot, branch string) string {
	return filepath.Join(worktreeBaseDir(repoRoot), strings.ReplaceAll(branch, "/", "+"))
}

// validBranchOrBaseName allows the conservative subset of refname
// characters Canopy needs: letters, digits, slashes for namespaces,
// and the dash/dot/underscore punctuation. It explicitly rejects
// names containing ".." (path traversal in the worktree base dir),
// ":" (gitref syntax), and any leading dash (so `--upload-pack=…`
// style flag injection through a positional arg is impossible).
func validBranchOrBaseName(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '/' || r == '_' || r == '.' || r == '-':
		default:
			return false
		}
	}
	return true
}

func createWorktreeCmd(repoRoot, branch, base string) tea.Cmd {
	return func() tea.Msg {
		if repoRoot == "" {
			return worktreeCreatedMsg{branch: branch, err: fmt.Errorf("repo root unknown")}
		}
		if !validBranchOrBaseName(branch) {
			return worktreeCreatedMsg{branch: branch, err: fmt.Errorf("invalid branch name %q (allowed: [A-Za-z0-9._/-], no leading '-', no '..')", branch)}
		}
		if !validBranchOrBaseName(base) {
			return worktreeCreatedMsg{branch: branch, err: fmt.Errorf("invalid base name %q (allowed: [A-Za-z0-9._/-], no leading '-', no '..')", base)}
		}
		path := WorktreePath(repoRoot, branch)
		if isDemoMode() {
			return worktreeCreatedMsg{branch: branch, path: path}
		}
		if err := os.MkdirAll(worktreeBaseDir(repoRoot), 0o755); err != nil {
			return worktreeCreatedMsg{branch: branch, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// `--` separates options from positional args so a path that
		// somehow starts with '-' can never be parsed as a flag.
		c := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", "-b", branch, "--", path, base)
		out, err := c.CombinedOutput()
		if err != nil {
			return worktreeCreatedMsg{branch: branch, path: path, err: fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))}
		}
		return worktreeCreatedMsg{branch: branch, path: path}
	}
}
