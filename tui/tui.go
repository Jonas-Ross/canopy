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
	"github.com/jonasross/canopy/analytics"
	"github.com/jonasross/canopy/sessions"
)

// Refresher triggers a data refresh and exposes the session store for
// analytical queries. Production passes *aggregator.Aggregator; tests
// inject a fake.
type Refresher interface {
	Refresh()
	SessionStore() *sessions.Store
}

// AnalyticsLoadedMsg carries the result of an async analytics.Build call.
// Dispatched when the forensics tab loads or when r is pressed on that
// tab. Exactly one of Snapshot or Err is meaningful per message — on
// failure the Update handler surfaces Err as a notice and flips
// analyticsLoaded so the user moves out of the "loading…" placeholder.
type AnalyticsLoadedMsg struct {
	Snapshot analytics.Snapshot
	Err      error
}

// UpdateMsg wraps an aggregator.Update for delivery via tea.Program.Send.
type UpdateMsg aggregator.Update

// blinkInterval is the half-period of the live-indicator blink. The tick
// fires every interval and toggles m.blinkPhase, so full on/off cycle is
// 2 * blinkInterval. Tuned by eye against canopy demo — 1s reads as a calm
// breathing rhythm; faster feels restless, slower feels sluggish.
const blinkInterval = 1 * time.Second

// tab is the top-level tab enum. tabOperational is the zero value so an
// uninitialized Model defaults to the operational view.
type tab int

const (
	tabOperational tab = iota
	tabForensics
)

// Model is the root bubbletea model. Update is pure: I/O lives in Run.
type Model struct {
	refresher Refresher

	tab tab

	// runCtx is the bubbletea-level lifecycle ctx, threaded through to the
	// op cmd factories (e.g. removeWorktreeCmd) so that quit-mid-operation
	// cancels the subprocess. NewModel defaults to context.Background();
	// Run overrides it before tea.NewProgram. Stored on the struct (against
	// the general "don't put contexts in structs" guidance / staticcheck
	// SA1029) because tea.Cmd factories run from inside Update, which has
	// no other way to reach the run-level ctx.
	runCtx context.Context

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

	// blinkPhase is the current phase of the live-indicator blink: true =
	// on (bright green ●), false = off (dim green ●). Toggled by the
	// self-rescheduling blinkTickMsg; the renderer reads it to pick a style.
	// blinkRunning gates against double-starting the tick when a second
	// Live worktree arrives while the first is still ticking.
	blinkPhase   bool
	blinkRunning bool

	procsExpanded map[string]bool

	// forensicsView is the active sub-tab within the forensics tab.
	// Zero value is viewSpend. Persists across tab round-trips so the user
	// returns to the same sub-view after switching back from ops.
	forensicsView forensicsView

	// analytics holds the most recently built analytics snapshot. Populated
	// asynchronously via loadAnalyticsCmd when the forensics tab is entered
	// or when r is pressed on that tab.
	analytics       analytics.Snapshot
	analyticsLoaded bool

	now func() time.Time
}

// NewModel constructs the root Model with the given Refresher. runCtx
// defaults to context.Background() so tests can construct a Model without
// caring about lifecycle plumbing; Run injects the real ctx in production.
func NewModel(r Refresher) tea.Model {
	ti := textinput.New()
	ti.Prompt = filterPrompt
	return Model{
		refresher:     r,
		runCtx:        context.Background(),
		width:         80,
		states:        make(map[string]aggregator.WorktreeState),
		procsExpanded: make(map[string]bool),
		filterInput:   ti,
		now:           time.Now,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

// refreshCmd wraps a Refresher.Refresh() call in a tea.Cmd so the
// bubbletea event loop never blocks on the refresh contract. Production's
// aggregator is non-blocking today, but the interface doesn't promise it.
func refreshCmd(r Refresher) tea.Cmd {
	return func() tea.Msg {
		r.Refresh()
		return nil
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case UpdateMsg:
		u := aggregator.Update(msg)
		// System notices (gh missing/unauthed) arrive with Worktree="" —
		// surface as a transient notice and skip the state-map path.
		if u.SystemNotice != "" {
			m.notice = errorStyle.Render(u.SystemNotice)
			return m, nil
		}
		_, existed := m.states[u.Worktree]
		if !existed {
			m.ordered = append(m.ordered, u.Worktree)
		}
		// Snapshot the no-Live → some-Live transition before mutating the
		// state map, so a fresh arrival can reset blinkPhase even when a
		// stale tick is still in flight (blinkRunning=true) from a previous
		// Live that has since dropped.
		prevAnyLive := m.anyLive()
		m.states[u.Worktree] = u.State
		if m.repo == "" && u.State.Repo.Name != "" {
			m.repo = u.State.Repo.Name
		}
		if m.repoRoot == "" && u.State.Repo.Root != "" {
			m.repoRoot = u.State.Repo.Root
		}
		// Reset blinkPhase to bright whenever a worktree transitions the
		// model from "no Live anywhere" to "some Live" — guarantees the
		// first paint of a freshly-arrived Live row is on-phase regardless
		// of where the in-flight tick chain is. Kick the tick when no chain
		// is running yet; otherwise the existing chain picks up the new
		// worktree on its next fire.
		if u.State.Live != nil && !prevAnyLive {
			m.blinkPhase = true
			if !m.blinkRunning {
				m.blinkRunning = true
				return m, tea.Tick(blinkInterval, func(time.Time) tea.Msg { return blinkTickMsg{} })
			}
		}
		return m, nil

	case blinkTickMsg:
		// Toggle the phase, then reschedule only if some worktree is still
		// Live. When the last Live worktree drops, the tick self-terminates
		// and idle CPU returns to baseline.
		m.blinkPhase = !m.blinkPhase
		if m.anyLive() {
			return m, tea.Tick(blinkInterval, func(time.Time) tea.Msg { return blinkTickMsg{} })
		}
		m.blinkRunning = false
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
			m.notice = errorStyle.Render("open shell tab failed: " + msg.err.Error())
		}
		return m, nil

	case worktreeRemovedMsg:
		if msg.err != nil {
			m.notice = errorStyle.Render("prune failed: " + msg.err.Error())
			return m, nil
		}
		m.notice = noticeStyle.Render("pruned " + msg.path)
		// Optimistically drop the row locally: aggregator.refreshAll
		// purges the state from its map but never broadcasts a deletion,
		// and UpdateMsg only upserts. Without this the pruned worktree
		// would linger in the list (still focusable, still actionable)
		// until a TUI restart.
		m = m.removeWorktree(msg.path)
		return m, refreshCmd(m.refresher)

	case worktreeCreatedMsg:
		if msg.err != nil {
			m.notice = errorStyle.Render("create failed: " + msg.err.Error())
			return m, nil
		}
		m.notice = noticeStyle.Render("created " + msg.branch + " at " + msg.path)
		return m, refreshCmd(m.refresher)

	case procsKilledMsg:
		switch {
		case msg.err != nil && msg.count == 0:
			m.notice = errorStyle.Render("kill failed: " + msg.err.Error())
		case msg.err != nil:
			m.notice = noticeStyle.Render(fmt.Sprintf("sent SIGTERM to %d procs (some errored)", msg.count))
		case msg.skipped > 0 && msg.count == 0:
			m.notice = errorStyle.Render(fmt.Sprintf("kill skipped: %d procs no longer in worktree (PID reuse?)", msg.skipped))
		case msg.skipped > 0:
			m.notice = noticeStyle.Render(fmt.Sprintf("sent SIGTERM to %d procs (%d skipped — cwd changed)", msg.count, msg.skipped))
		default:
			m.notice = noticeStyle.Render(fmt.Sprintf("sent SIGTERM to %d procs", msg.count))
		}
		return m, refreshCmd(m.refresher)

	case AnalyticsLoadedMsg:
		// Error path: surface as a notice and flip analyticsLoaded so the
		// loading placeholder doesn't sit forever. Keep the previous
		// snapshot (if any) visible behind the notice. Retry via r.
		if msg.Err != nil {
			m.notice = errorStyle.Render("analytics: " + msg.Err.Error())
			m.analyticsLoaded = true
			return m, nil
		}
		if !msg.Snapshot.GeneratedAt.IsZero() {
			m.analytics = msg.Snapshot
			m.analyticsLoaded = true
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Any keypress dismisses a pending notice. Handlers may set a new notice
	// after this; that new value sticks until the *next* keypress. Form mode
	// is the exception: m.notice carries the form-validation error there and
	// must persist across keystrokes so the user has time to read it (the
	// form handler clears it explicitly on Esc / successful create).
	if m.mode != modeNewWorktree {
		m.notice = ""
	}

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
	// Route forensics-tab keys to the dedicated handler so digit/h/l
	// bindings never interfere with the operational tab.
	if m.tab == tabForensics {
		return m.updateForensicsMode(msg)
	}

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
		case keyProcsToggle:
			m = m.toggleFocusedProcsExpansion()
		}

	case msg.Type == tea.KeyDown:
		m = m.moveFocus(1)

	case msg.Type == tea.KeyUp:
		m = m.moveFocus(-1)

	case msg.Type == tea.KeyTab:
		// Only reached when m.tab == tabOperational — forensics Tab is
		// handled by updateForensicsMode before reaching this switch.
		m.tab = tabForensics
		return m, loadAnalyticsCmd(m.refresher, m.now())

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
		m = m.snapFocusToVisible()
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
		return m, removeWorktreeCmd(m.runCtx, state.Worktree.Path)
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
		return m, killProcsCmd(pids, state.Worktree.Path)
	})
	return next, cmd
}

// snapFocusToVisible moves focusIndex to the first worktree that passes
// the current filter, when the existing focus is hidden. No-op when the
// filter is empty or focus is already on a visible row.
func (m Model) snapFocusToVisible() Model {
	filter := m.activeFilter()
	if filter == "" {
		return m
	}
	lowerFilter := strings.ToLower(filter)
	visible := func(path string) bool {
		state, ok := m.states[path]
		if !ok {
			return false
		}
		branch := FormatBranch(state.Worktree.Branch, state.Worktree.Detached)
		return strings.Contains(strings.ToLower(branch), lowerFilter)
	}
	if m.focusIndex >= 0 && m.focusIndex < len(m.ordered) && visible(m.ordered[m.focusIndex]) {
		return m
	}
	for i, path := range m.ordered {
		if visible(path) {
			m.focusIndex = i
			return m
		}
	}
	return m
}

// removeWorktree drops path from m.states and m.ordered and clamps the
// focus to a still-valid row. Safe to call on a path that isn't tracked.
func (m Model) removeWorktree(path string) Model {
	if _, ok := m.states[path]; !ok {
		return m
	}
	delete(m.states, path)
	delete(m.procsExpanded, path)
	for i, p := range m.ordered {
		if p == path {
			m.ordered = append(m.ordered[:i], m.ordered[i+1:]...)
			break
		}
	}
	if m.focusIndex >= len(m.ordered) {
		m.focusIndex = len(m.ordered) - 1
	}
	if m.focusIndex < 0 {
		m.focusIndex = 0
	}
	return m
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

func (m Model) toggleFocusedProcsExpansion() Model {
	state, ok := m.focusedState()
	if !ok {
		return m
	}
	m.procsExpanded[state.Worktree.Path] = !m.procsExpanded[state.Worktree.Path]
	return m
}

// anyLive reports whether at least one tracked worktree has a live session.
// Used by the blink tick to decide whether to reschedule itself.
func (m Model) anyLive() bool {
	for _, s := range m.states {
		if s.Live != nil {
			return true
		}
	}
	return false
}

func (m Model) activeFilter() string {
	if m.mode == modeFiltering {
		return m.filterInput.Value()
	}
	return m.filterStr
}

// wrappedRows returns the on-screen row count for s at the given column
// width, accounting for terminal soft-wrap. lipgloss.Height counts only
// explicit newlines, which undercounts the new-worktree footer and the
// title/footer rule-fills (they overshoot width by 1–4 chars).
func wrappedRows(s string, width int) int {
	if width <= 0 {
		return lipgloss.Height(s)
	}
	rows := 0
	for _, line := range strings.Split(s, "\n") {
		w := lipgloss.Width(line)
		if w == 0 {
			rows++
			continue
		}
		rows += (w + width - 1) / width
	}
	return rows
}

func (m Model) View() string {
	switch m.tab {
	case tabForensics:
		return m.renderForensicsView()
	default:
		return m.renderOperationalView()
	}
}

func (m Model) renderOperationalView() string {
	now := m.now()
	width := m.width
	if width <= 0 {
		width = 80
	}

	title := m.renderTitleBar(width)

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

	list := renderWorktreeList(m.ordered, m.states, m.focusIndex, m.activeFilter(), now, listW, m.blinkPhase)

	footer := m.renderFooter(width)

	// Pin the footer to the bottom so the layout has a stable height —
	// otherwise body-height changes (focus moving onto a tall detail pane,
	// items appearing under a filter) make the alt-screen redraw at a
	// different row count and the footer visibly jumps. The body is either
	// stretched (detail pane present → pane border runs full height) or
	// padded below (list-only). Skip when m.height is unset.
	bodyTargetH := 0
	if m.height > 0 {
		bodyTargetH = m.height - wrappedRows(title, width) - 1 - 1 - wrappedRows(footer, width)
	}

	body := list
	if showDetail {
		body = layoutWithDetail(list, renderDetailPane(focused, now, bodyTargetH, m.procsExpanded[focused.Worktree.Path]), width)
	}

	var pad string
	if bodyTargetH > 0 {
		if extra := bodyTargetH - wrappedRows(body, width); extra > 0 {
			pad = strings.Repeat("\n", extra)
		}
	}

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("\n\n")
	sb.WriteString(body)
	sb.WriteString(pad)
	sb.WriteString("\n\n")
	sb.WriteString(footer)
	return sb.String()
}

func (m Model) renderTitleBar(width int) string {
	left := " " + titleStyle.Render("Canopy")
	if m.repo != "" {
		left += " " + ruleStyle.Render("·") + " " + repoStyle.Render(m.repo)
	}

	opsStyle := tabFaded
	forensicsStyle := tabFaded
	if m.tab == tabForensics {
		forensicsStyle = tabActive
	} else {
		opsStyle = tabActive
	}
	right := opsStyle.Render("ops") + " " + tabFaded.Render("·") + " " + forensicsStyle.Render("forensics") + " "

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
		// A pending validation error displaces the keybind hint — once the
		// user has invoked Enter they need the feedback more than the prompt.
		var trailing string
		if m.notice != "" {
			trailing = m.notice
		} else {
			trailing = keyDescStyle.Render("[tab] switch  [enter] create  [esc] cancel")
		}
		return "  " + promptStyle.Render("new worktree") + "    " + m.newBranchInput.View() + "    " + m.newBaseInput.View() + "    " + trailing
	}

	// Transient notice (errors, action results, f/tab stubs). Cleared on the
	// next keypress by handleKey.
	if m.notice != "" {
		return "  " + m.notice
	}

	// Prune is structurally invalid on the primary worktree (git itself
	// refuses), so omit it from the help footer when that row is focused.
	focused, hasFocus := m.focusedState()
	hidePrune := hasFocus && focused.Worktree.Main

	var chunks []string
	for _, b := range footerKeys {
		if hidePrune && b.key == string(keyPrune) {
			continue
		}
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

	m := NewModel(agg).(Model)
	m.runCtx = ctx
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
// frame harness to freeze time so relative-time and notice rendering is
// deterministic.
func SetNow(m tea.Model, fn func() time.Time) tea.Model {
	if mm, ok := m.(Model); ok {
		mm.now = fn
		return mm
	}
	return m
}

// SetNotice replaces a Model's transient notice string. Test-only seam
// for verifying that footers (ops and forensics) render m.notice
// without having to thread the actual async-op cmd flow.
func SetNotice(m tea.Model, notice string) tea.Model {
	if mm, ok := m.(Model); ok {
		mm.notice = notice
		return mm
	}
	return m
}
