// Package tui implements the bubbletea TUI operational view for Canopy.
package tui

import (
	"context"
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

// Model is the root bubbletea model. Update is pure: I/O lives in Run.
type Model struct {
	refresher Refresher

	repo string

	width  int
	height int

	ordered []string
	states  map[string]aggregator.WorktreeState

	focusIndex int

	filtering   bool
	filterInput textinput.Model
	filterStr   string

	footer string

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
		footer:      footerHelp,
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
		if _, exists := m.states[u.Worktree]; !exists {
			m.ordered = append(m.ordered, u.Worktree)
		}
		m.states[u.Worktree] = u.State
		// Derive repo name from the first incoming update.
		if m.repo == "" && u.State.Repo.Name != "" {
			m.repo = u.State.Repo.Name
		}
		return m, nil

	case tea.KeyMsg:
		if m.filtering {
			return m.updateFilterInput(msg)
		}
		return m.updateNormalMode(msg)
	}

	return m, nil
}

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
		case keyForensics:
			m.footer = footerForensics
		}

	case msg.Type == tea.KeyDown:
		m = m.moveFocus(1)

	case msg.Type == tea.KeyUp:
		m = m.moveFocus(-1)

	case msg.Type == tea.KeyTab:
		m.footer = footerTab

	case msg.Type == tea.KeyEsc:
		m.filterStr = ""
		m.footer = footerHelp
	}

	return m, nil
}

func (m Model) updateFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.filterStr = m.filterInput.Value()
		m.filtering = false
		m.filterInput.Blur()
		m.footer = footerHelp
		return m, nil

	case tea.KeyEsc:
		m.filterStr = ""
		m.filterInput.SetValue("")
		m.filtering = false
		m.filterInput.Blur()
		m.footer = footerHelp
		return m, nil

	default:
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		return m, cmd
	}
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

func (m Model) View() string {
	now := m.now()
	width := m.width
	if width <= 0 {
		width = 80
	}

	var sb strings.Builder

	sb.WriteString(m.renderTitleBar(width))
	sb.WriteString("\n\n")

	sb.WriteString(renderWorktreeList(m.ordered, m.states, m.focusIndex, m.activeFilter(), now))
	sb.WriteString("\n\n")

	sb.WriteString(m.renderFooter(width))

	return sb.String()
}

func (m Model) activeFilter() string {
	if m.filtering {
		return m.filterInput.Value()
	}
	return m.filterStr
}

func (m Model) renderTitleBar(width int) string {
	// "── Canopy · <repo> ─────────────── ops ──"
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
	if m.filtering {
		return "  " + m.filterInput.View()
	}
	// Transient footer set by f/tab.
	if m.footer != footerHelp {
		return "  " + dimStyle.Render(m.footer)
	}
	// Default styled help footer.
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
		return mm.filtering
	}
	return false
}

func FilterValue(m tea.Model) string {
	if mm, ok := m.(Model); ok {
		return mm.filterStr
	}
	return ""
}
