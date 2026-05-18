package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/internal/ansi"
	"github.com/jonasross/canopy/tui"
)

// Script directive kinds. Keep these strings in sync with docs/validation.md.
const (
	dirWidth      = "width"
	dirHeight     = "height"
	dirKeys       = "keys"
	dirKey        = "key"
	dirWait       = "wait"
	dirResolve    = "resolve"
	dirCapture    = "capture"
	dirCapturePNG = "capture-png"
	dirNote       = "note"
)

// runScript replays a directive file against the live aggregator + a freshly
// built Model.
//
// Cmd-cascade timing matters here: a `keys p` followed immediately by a
// `capture` should snapshot the *post-keypress, pre-cascade* state — that's
// the moment the notice ("opening …", "no PR for main", etc.) is visible.
// We therefore queue any tea.Cmd returned from Update and flush it only at a
// `wait` directive or end-of-script. The flush executes with a short timeout
// so timer-based commands (tea.Tick for pulse expiry) don't block.
func runScript(ctx context.Context, cmd *cobra.Command, agg *aggregator.Aggregator, scriptPath string, width, height int) error {
	directives, err := parseScript(scriptPath)
	if err != nil {
		return fmt.Errorf("canopy demo: parse script %s: %w", scriptPath, err)
	}

	m := tui.NewModel(agg)
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})

	updates := agg.Subscribe(ctx)
	defer agg.Close()

	state := &scriptState{
		m: m, ctx: ctx, cobraCmd: cmd, updates: updates,
		width: width, height: height,
	}

	// Brief settle for the aggregator baseline.
	time.Sleep(50 * time.Millisecond)
	state.drainAggregator()

	for i, d := range directives {
		if err := applyDirective(state, d); err != nil {
			return fmt.Errorf("canopy demo: directive %d (%s): %w", i+1, d.kind, err)
		}
		state.drainAggregator()
	}
	state.flushPending()
	return nil
}

// scriptState carries the per-run state across directive applications.
type scriptState struct {
	m        tea.Model
	ctx      context.Context
	cobraCmd *cobra.Command
	updates  <-chan aggregator.Update

	// width and height track the last applied window size so width/height
	// directives can update one dimension without resetting the other.
	width, height int

	// pending is the last tea.Cmd returned from Update that has not yet
	// been resolved. flushed on wait / end-of-script.
	pending tea.Cmd
}

func (s *scriptState) drainAggregator() {
	for {
		select {
		case u, ok := <-s.updates:
			if !ok {
				return
			}
			s.m, _ = s.m.Update(tui.UpdateMsg(u))
		default:
			return
		}
	}
}

// sendMsg delivers msg to the Model, replacing any pending cmd with whatever
// Update returns. Callers must remember that the new cmd is queued, not run.
func (s *scriptState) sendMsg(msg tea.Msg) {
	next, cmd := s.m.Update(msg)
	s.m = next
	s.pending = cmd
}

// flushPending resolves any queued tea.Cmd and feeds its result back through
// Update, with a short timeout so timer-based commands don't block.
func (s *scriptState) flushPending() {
	c := s.pending
	s.pending = nil
	if c == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- nil
			}
		}()
		done <- c()
	}()
	select {
	case msg := <-done:
		if msg == nil {
			return
		}
		next, follow := s.m.Update(msg)
		s.m = next
		s.pending = follow
		s.flushPending()
	case <-time.After(150 * time.Millisecond):
		// Likely a tea.Tick. Drop it.
	}
}

type scriptDirective struct {
	line int
	kind string
	arg  string
}

func parseScript(path string) ([]scriptDirective, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []scriptDirective
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		kind, arg, _ := strings.Cut(raw, " ")
		out = append(out, scriptDirective{line: lineNo, kind: kind, arg: strings.TrimSpace(arg)})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func applyDirective(s *scriptState, d scriptDirective) error {
	switch d.kind {
	case dirWidth, dirHeight:
		n, err := parseInt(d.arg)
		if err != nil {
			return err
		}
		if d.kind == dirWidth {
			s.width = n
		} else {
			s.height = n
		}
		s.sendMsg(tea.WindowSizeMsg{Width: s.width, Height: s.height})
		return nil

	case dirKeys:
		for _, r := range d.arg {
			s.flushPending() // resolve prior cmd before each new keypress
			s.sendMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		return nil

	case dirKey:
		typ, ok := namedKey(d.arg)
		if !ok {
			return fmt.Errorf("unknown named key %q (want one of: enter, esc, tab, shift-tab, up, down, left, right, backspace, space, ctrl-c)", d.arg)
		}
		s.flushPending()
		s.sendMsg(tea.KeyMsg{Type: typ})
		return nil

	case dirWait:
		dur, err := time.ParseDuration(d.arg)
		if err != nil {
			return err
		}
		s.flushPending()
		time.Sleep(dur)
		return nil

	case dirResolve:
		s.flushPending()
		return nil

	case dirCapture:
		return writeFrame(d.arg, ansi.Strip(s.m.View()))

	case dirCapturePNG:
		return captureFramePNG(s.ctx, d.arg, s.m.View())

	case dirNote:
		fmt.Fprintln(s.cobraCmd.ErrOrStderr(), "demo:", d.arg)
		return nil

	default:
		return fmt.Errorf("unknown directive %q", d.kind)
	}
}

func parseInt(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("expected integer, got %q: %w", s, err)
	}
	return n, nil
}

func namedKey(name string) (tea.KeyType, bool) {
	switch strings.ToLower(name) {
	case "enter", "return":
		return tea.KeyEnter, true
	case "esc", "escape":
		return tea.KeyEsc, true
	case "tab":
		return tea.KeyTab, true
	case "shift-tab", "shifttab":
		return tea.KeyShiftTab, true
	case "up":
		return tea.KeyUp, true
	case "down":
		return tea.KeyDown, true
	case "left":
		return tea.KeyLeft, true
	case "right":
		return tea.KeyRight, true
	case "backspace":
		return tea.KeyBackspace, true
	case "space":
		return tea.KeySpace, true
	case "ctrl-c", "ctrlc":
		return tea.KeyCtrlC, true
	}
	return 0, false
}

// writeFrame writes content to path, creating parent directories as needed.
func writeFrame(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func captureFramePNG(ctx context.Context, pngPath, view string) error {
	tmp, err := os.CreateTemp("", "canopy-demo-frame-*.ansi")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(view); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pngPath), 0o755); err != nil {
		return err
	}
	return renderPNG(ctx, tmp.Name(), pngPath)
}
