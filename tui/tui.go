// Package tui implements the bubbletea TUI operational view for Canopy.
// M4 slices 1-3: worktree list, live-agent indicator, filter input.
package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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
// Update is a pure function: all I/O lives outside it.
type Model struct {
	refresher Refresher

	// ordered preserves insertion order for deterministic rendering.
	ordered []string
	states  map[string]aggregator.WorktreeState

	focusIndex int

	// filter state
	filtering   bool
	filterInput textinput.Model
	filterStr   string // committed filter

	// footer message — overridden transiently by f/tab keys
	footer string

	// injectable clock so tests can fix time without touching the system clock
	now func() time.Time
}

// NewModel constructs the root Model with the given Refresher. Tests call
// this directly; Run constructs it internally.
func NewModel(r Refresher) tea.Model {
	ti := textinput.New()
	ti.Prompt = filterPrompt
	return Model{
		refresher:   r,
		states:      make(map[string]aggregator.WorktreeState),
		filterInput: ti,
		footer:      footerHelp,
		now:         time.Now,
	}
}

// Init implements tea.Model. No initial command needed; the bridge
// goroutine in Run forwards aggregator updates.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model. Pure: no I/O, no side effects beyond
// calling m.refresher.Refresh() on 'r'.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case UpdateMsg:
		u := aggregator.Update(msg)
		if _, exists := m.states[u.Worktree]; !exists {
			m.ordered = append(m.ordered, u.Worktree)
		}
		m.states[u.Worktree] = u.State
		return m, nil

	case tea.KeyMsg:
		// When the filter input is active, route keys through it first.
		if m.filtering {
			return m.updateFilterInput(msg)
		}
		return m.updateNormalMode(msg)
	}

	return m, nil
}

// updateNormalMode handles key messages when not in filter-input mode.
func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m, tea.Quit

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
			m.filtering = true
			m.filterInput.SetValue(m.filterStr)
			m.filterInput.Focus()
			m.footer = filterPrompt

		case keyForensics:
			m.footer = footerForensics

		default:
			// unrecognized rune — no-op
		}

	case msg.Type == tea.KeyDown:
		m = m.moveFocus(1)

	case msg.Type == tea.KeyUp:
		m = m.moveFocus(-1)

	case msg.Type == tea.KeyTab:
		m.footer = footerTab

	case msg.Type == tea.KeyEsc:
		// Clear committed filter from normal mode.
		m.filterStr = ""
		m.filterInput.SetValue("")
		m.footer = footerHelp
	}

	return m, nil
}

// updateFilterInput handles key messages while the filter textinput is active.
func (m Model) updateFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		// Commit the filter and leave filter mode.
		m.filterStr = m.filterInput.Value()
		m.filtering = false
		m.filterInput.Blur()
		m.footer = footerHelp
		return m, nil

	case tea.KeyEsc:
		// Clear the filter and leave filter mode.
		m.filterStr = ""
		m.filterInput.SetValue("")
		m.filtering = false
		m.filterInput.Blur()
		m.footer = footerHelp
		return m, nil

	default:
		// Forward to the textinput component.
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		return m, cmd
	}
}

// moveFocus adjusts the focus index by delta, clamped to [0, len-1].
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

// View implements tea.Model.
func (m Model) View() string {
	now := m.now()

	var sb strings.Builder

	// Tabs strip — single tab placeholder in v1.
	sb.WriteString(dimStyle.Render("[ops]"))
	sb.WriteString("\n\n")

	// Worktree list.
	filter := m.filterStr
	if m.filtering {
		filter = m.filterInput.Value()
	}
	sb.WriteString(renderWorktreeList(m.ordered, m.states, m.focusIndex, filter, now))
	sb.WriteString("\n\n")

	// Filter input or footer.
	if m.filtering {
		sb.WriteString(m.filterInput.View())
	} else {
		sb.WriteString(dimStyle.Render(m.footer))
	}

	return sb.String()
}

// Run constructs the bubbletea program, wires the aggregator subscription
// bridge goroutine, and blocks until the program exits or ctx is cancelled.
// On exit, agg.Close() is called.
func Run(ctx context.Context, agg *aggregator.Aggregator) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := NewModel(agg)
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Bridge goroutine: consume aggregator updates and forward to bubbletea.
	ch := agg.Subscribe(ctx)
	go func() {
		for u := range ch {
			p.Send(UpdateMsg(u))
		}
		// Channel closed (ctx done or agg closed): quit the program.
		p.Send(tea.QuitMsg{})
	}()

	_, err := p.Run()
	cancel()
	agg.Close()
	return err
}

// FocusIndex returns the currently focused row index. Exported for tests.
func FocusIndex(m tea.Model) int {
	if mm, ok := m.(Model); ok {
		return mm.focusIndex
	}
	return 0
}

// IsFiltering reports whether the model is in filter-input mode. Exported for tests.
func IsFiltering(m tea.Model) bool {
	if mm, ok := m.(Model); ok {
		return mm.filtering
	}
	return false
}

// FilterValue returns the current committed filter string. Exported for tests.
func FilterValue(m tea.Model) string {
	if mm, ok := m.(Model); ok {
		return mm.filterStr
	}
	return ""
}
