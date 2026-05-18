// Package tui implements the bubbletea TUI operational view for Canopy.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonasross/canopy/aggregator"
)

// Refresher triggers a data refresh. Production passes *aggregator.Aggregator;
// tests inject a fake.
type Refresher interface {
	Refresh()
}

// UpdateMsg wraps an aggregator.Update for delivery via tea.Program.Send.
type UpdateMsg aggregator.Update

// pulseDuration is how long a freshly-arrived live update is highlighted.
const pulseDuration = 600 * time.Millisecond

// Model is the root bubbletea model. Update is pure: I/O lives in Run.
type Model struct {
	refresher Refresher

	repo     string
	repoRoot string

	width  int
	height int

	ordered []string
	states  map[string]aggregator.WorktreeState

	focusIndex int

	mode mode

	filterInput textinput.Model
	filterStr   string

	newBranchInput textinput.Model
	newBaseInput   textinput.Model
	newFormFocus   int

	notice string

	pulsePath  string
	pulseUntil time.Time

	now func() time.Time
}

// NewModel constructs the root Model with the given Refresher.
func NewModel(r Refresher) tea.Model {
	ti := textinput.New()
	ti.Prompt = filterPrompt
	return Model{
		refresher:   r,
		width:       80,
		states:      make(map[string]aggregator.WorktreeState),
		filterInput: ti,
		now:         time.Now,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case UpdateMsg:
		u := aggregator.Update(msg)
		prev, existed := m.states[u.Worktree]
		if !existed {
			m.ordered = append(m.ordered, u.Worktree)
		}
		m.states[u.Worktree] = u.State
		if m.repo == "" && u.State.Repo.Name != "" {
			m.repo = u.State.Repo.Name
		}
		if m.repoRoot == "" && u.State.Repo.Root != "" {
			m.repoRoot = u.State.Repo.Root
		}
		// Pulse only on subsequent updates — initial load delivers all
		// existing live sessions in one burst and shouldn't flash them.
		if existed && u.State.Live != nil && (prev.Live == nil || u.State.Live.UpdatedAt.After(prev.Live.UpdatedAt)) {
			m.pulsePath = u.Worktree
			m.pulseUntil = m.now().Add(pulseDuration)
			return m, tea.Tick(pulseDuration, func(time.Time) tea.Msg { return pulseExpiredMsg{} })
		}
		return m, nil

	case pulseExpiredMsg:
		return m, nil

	case prOpenedMsg:
		if msg.err != nil {
			m.notice = errorStyle.Render("open PR failed: " + msg.err.Error())
		} else {
			// browser opened — dismiss the "opening..." placeholder
			m.notice = ""
		}
		return m, nil

	case shellDroppedMsg:
		if msg.err != nil {
			m.notice = errorStyle.Render("shell exited with error: " + msg.err.Error())
		}
		return m, nil

	case worktreeRemovedMsg:
		if msg.err != nil {
			m.notice = errorStyle.Render("prune failed: " + msg.err.Error())
		} else {
			m.notice = noticeStyle.Render("pruned " + msg.path)
			m.refresher.Refresh()
		}
		return m, nil

	case worktreeCreatedMsg:
		if msg.err != nil {
			m.notice = errorStyle.Render("create failed: " + msg.err.Error())
		} else {
			m.notice = noticeStyle.Render("created " + msg.branch + " at " + msg.path)
			m.refresher.Refresh()
		}
		return m, nil

	case procsKilledMsg:
		switch {
		case msg.err != nil && msg.count == 0:
			m.notice = errorStyle.Render("kill failed: " + msg.err.Error())
		case msg.err != nil:
			m.notice = noticeStyle.Render(fmt.Sprintf("sent SIGTERM to %d procs (some errored)", msg.count))
		default:
			m.notice = noticeStyle.Render(fmt.Sprintf("sent SIGTERM to %d procs", msg.count))
		}
		m.refresher.Refresh()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Any keypress dismisses a pending notice. Handlers may set a new notice
	// after this; that new value sticks until the *next* keypress.
	m.notice = ""

	switch m.mode {
	case modeFiltering:
		return m.updateFilterInput(msg)
	case modeConfirmPrune:
		return m.updateConfirmPrune(msg)
	case modeConfirmKill:
		return m.updateConfirmKill(msg)
	case modeNewWorktree:
		out, cmd := m.updateNewWorktreeForm(msg)
		return out, cmd
	}
	return m.updateNormalMode(msg)
}

func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m, tea.Quit

	case msg.Type == tea.KeyEnter:
		out, cmd := m.handleShellDrop()
		return out, cmd

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1:
		switch msg.Runes[0] {
		case keyQuit:
			return m, tea.Quit
		case keyDown:
			m = m.moveFocus(1)
		case keyUp:
			m = m.moveFocus(-1)
		case keyRefresh:
			m.refresher.Refresh()
		case keyFilter:
			m.mode = modeFiltering
			m.filterInput.SetValue(m.filterStr)
			m.filterInput.Focus()
		case keyForensics:
			m.notice = dimStyle.Render(footerForensics)
		case keyNew:
			out, cmd := m.startNewWorktree()
			return out, cmd
		case keyPrune:
			out, cmd := m.startPrune()
			return out, cmd
		case keyOpenPR:
			out, cmd := m.handleOpenPR()
			return out, cmd
		case keyKill:
			out, cmd := m.startKill()
			return out, cmd
		}

	case msg.Type == tea.KeyDown:
		m = m.moveFocus(1)

	case msg.Type == tea.KeyUp:
		m = m.moveFocus(-1)

	case msg.Type == tea.KeyTab:
		m.notice = dimStyle.Render(footerTab)

	case msg.Type == tea.KeyEsc:
		m.filterStr = ""
	}

	return m, nil
}

func (m Model) updateFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.filterStr = m.filterInput.Value()
		m.mode = modeNormal
		m.filterInput.Blur()
		return m, nil

	case tea.KeyEsc:
		m.filterStr = ""
		m.filterInput.SetValue("")
		m.mode = modeNormal
		m.filterInput.Blur()
		return m, nil

	default:
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		return m, cmd
	}
}

// confirmKey routes a y/Y/n/N/Esc keypress in a confirm-modal mode. When the
// user presses yes, onYes is invoked with the focused state and produces the
// resulting (Model, Cmd). Other keys cancel back to modeNormal or no-op.
func (m Model) confirmKey(msg tea.KeyMsg, onYes func(Model, aggregator.WorktreeState) (Model, tea.Cmd)) (Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		m.mode = modeNormal
		return m, nil
	}
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return m, nil
	}
	switch msg.Runes[0] {
	case 'y', 'Y':
		state, ok := m.focusedState()
		m.mode = modeNormal
		if !ok {
			return m, nil
		}
		return onYes(m, state)
	case 'n', 'N':
		m.mode = modeNormal
	}
	return m, nil
}

func (m Model) updateConfirmPrune(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	next, cmd := m.confirmKey(msg, func(m Model, state aggregator.WorktreeState) (Model, tea.Cmd) {
		m.notice = noticeStyle.Render("pruning " + FormatBranch(state.Worktree.Branch, state.Worktree.Detached) + "…")
		return m, removeWorktreeCmd(state.Worktree.Path)
	})
	return next, cmd
}

func (m Model) updateConfirmKill(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	next, cmd := m.confirmKey(msg, func(m Model, state aggregator.WorktreeState) (Model, tea.Cmd) {
		if len(state.Procs) == 0 {
			return m, nil
		}
		pids := make([]int, 0, len(state.Procs))
		for _, p := range state.Procs {
			pids = append(pids, p.Pid)
		}
		m.notice = noticeStyle.Render(fmt.Sprintf("sending SIGTERM to %d procs…", len(pids)))
		return m, killProcsCmd(pids)
	})
	return next, cmd
}

func (m Model) moveFocus(delta int) Model {
	n := len(m.ordered)
	if n == 0 {
		return m
	}
	next := m.focusIndex + delta
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - 1
	}
	m.focusIndex = next
	return m
}

func (m Model) activeFilter() string {
	if m.mode == modeFiltering {
		return m.filterInput.Value()
	}
	return m.filterStr
}

func (m Model) View() string {
	now := m.now()
	width := m.width
	if width <= 0 {
		width = 80
	}

	var sb strings.Builder

	sb.WriteString(m.renderTitleBar(width))
	sb.WriteString("\n\n")

	pulseFor := ""
	if !m.pulseUntil.IsZero() && now.Before(m.pulseUntil) {
		pulseFor = m.pulsePath
	}

	// Compute the list's effective width — it shrinks when the detail pane is
	// visible so column-visibility gates fire against the actual list area.
	listW := width
	focused, hasFocus := m.focusedState()
	showDetail := hasFocus && width >= detailPaneVisibleWidth
	if showDetail {
		if reduced := width - detailPaneWidth - 4; reduced >= 30 {
			listW = reduced
		} else {
			showDetail = false
		}
	}

	list := renderWorktreeList(m.ordered, m.states, m.focusIndex, m.activeFilter(), now, listW, pulseFor)

	if showDetail {
		sb.WriteString(layoutWithDetail(list, renderDetailPane(focused, now), width))
	} else {
		sb.WriteString(list)
	}
	sb.WriteString("\n\n")

	sb.WriteString(m.renderFooter(width))

	return sb.String()
}

func (m Model) renderTitleBar(width int) string {
	left := " " + titleStyle.Render("Canopy")
	if m.repo != "" {
		left += " " + ruleStyle.Render("·") + " " + repoStyle.Render(m.repo)
	}
	right := tabActive.Render("ops") + " " + tabFaded.Render("·") + " " + tabFaded.Render("forensics") + " "

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	fill := width - leftW - rightW - 2
	if fill < 1 {
		fill = 1
	}
	return ruleStyle.Render("──") + left + " " + ruleStyle.Render(strings.Repeat("─", fill)) + " " + right + ruleStyle.Render("──")
}

func (m Model) renderFooter(width int) string {
	// Modal footers take priority.
	switch m.mode {
	case modeFiltering:
		return "  " + m.filterInput.View()
	case modeConfirmPrune:
		state, _ := m.focusedState()
		return "  " + promptStyle.Render(fmt.Sprintf("prune %s? [y/N]", FormatBranch(state.Worktree.Branch, state.Worktree.Detached)))
	case modeConfirmKill:
		state, _ := m.focusedState()
		return "  " + promptStyle.Render(fmt.Sprintf("send SIGTERM to %d procs in %s? [y/N]", len(state.Procs), FormatBranch(state.Worktree.Branch, state.Worktree.Detached)))
	case modeNewWorktree:
		return "  " + promptStyle.Render("new worktree") + "    " + m.newBranchInput.View() + "    " + m.newBaseInput.View() + "    " + keyDescStyle.Render("[tab] switch  [enter] create  [esc] cancel")
	}

	// Transient notice (errors, action results, f/tab stubs). Cleared on the
	// next keypress by handleKey.
	if m.notice != "" {
		return "  " + m.notice
	}

	var chunks []string
	for _, b := range footerKeys {
		chunks = append(chunks, keyStyle.Render(b.key)+" "+keyDescStyle.Render(b.desc))
	}
	help := strings.Join(chunks, "  "+keyDescStyle.Render("·")+"  ")

	helpW := lipgloss.Width(help)
	fill := width - helpW - 4
	if fill < 1 {
		fill = 1
	}
	return "  " + help + " " + ruleStyle.Render(strings.Repeat("─", fill)) + "  "
}

// Run constructs the bubbletea program, bridges aggregator updates into it,
// and blocks until the program exits or ctx is cancelled. agg.Close() is
// called before returning.
func Run(ctx context.Context, agg *aggregator.Aggregator) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := NewModel(agg)
	p := tea.NewProgram(m, tea.WithAltScreen())

	ch := agg.Subscribe(ctx)
	go func() {
		for u := range ch {
			p.Send(UpdateMsg(u))
		}
		// Channel closed (ctx done or agg closed) — exit the program.
		// Safe no-op if the user already pressed q.
		p.Quit()
	}()

	_, err := p.Run()
	cancel()
	agg.Close()
	return err
}

// FocusIndex, IsFiltering, FilterValue are test seams that return zero values
// on a type-assertion miss. Production code reads Model fields directly.
func FocusIndex(m tea.Model) int {
	if mm, ok := m.(Model); ok {
		return mm.focusIndex
	}
	return 0
}

func IsFiltering(m tea.Model) bool {
	if mm, ok := m.(Model); ok {
		return mm.mode == modeFiltering
	}
	return false
}

func FilterValue(m tea.Model) string {
	if mm, ok := m.(Model); ok {
		return mm.filterStr
	}
	return ""
}

// SetNow replaces a Model's clock function. Test-only — used by the golden
// frame harness to freeze time so pulse/notice rendering is deterministic.
func SetNow(m tea.Model, fn func() time.Time) tea.Model {
	if mm, ok := m.(Model); ok {
		mm.now = fn
		return mm
	}
	return m
}
