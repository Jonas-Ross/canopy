package tui_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/analytics"
	"github.com/jonasross/canopy/sessions"
	"github.com/jonasross/canopy/tui"
)

// newEmptyStore creates an empty sessions.Store backed by a temporary
// directory that mimics the ~/.claude/projects/ layout but contains no
// JSONL files. Safe to use in tests that only need a valid (non-nil)
// store rather than real session data.
func newEmptyStore(t *testing.T) *sessions.Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("newEmptyStore: mkdir %s: %v", root, err)
	}
	store, err := sessions.Open(root)
	if err != nil {
		t.Fatalf("newEmptyStore: sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// storeRefresher is a Refresher that returns a real *sessions.Store.
// Used by analytics-load tests; the operational-tab tests continue to
// use the shared fakeRefresher (which returns a nil store — safe as long
// as no test executes the analytics cmd).
type storeRefresher struct {
	store *sessions.Store
}

func (s *storeRefresher) Refresh()                      {}
func (s *storeRefresher) SessionStore() *sessions.Store { return s.store }

// TestTabSwitchToForensics_returnsLoadCmd verifies that pressing Tab
// (ops → forensics) returns a non-nil tea.Cmd and that executing it
// returns an AnalyticsLoadedMsg.
func TestTabSwitchToForensics_returnsLoadCmd(t *testing.T) {
	store := newEmptyStore(t)
	m := tui.NewModel(&storeRefresher{store: store})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd after Tab to forensics, got nil")
	}

	msg := cmd()
	if _, ok := msg.(tui.AnalyticsLoadedMsg); !ok {
		t.Errorf("cmd() returned %T, want tui.AnalyticsLoadedMsg", msg)
	}
}

// TestAnalyticsLoadedMsg_storedOnModel verifies that dispatching an
// AnalyticsLoadedMsg with a non-zero GeneratedAt stores the snapshot and
// sets analyticsLoaded = true.
func TestAnalyticsLoadedMsg_storedOnModel(t *testing.T) {
	m := tui.NewModel(&storeRefresher{store: newEmptyStore(t)})

	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	m, _ = m.Update(tui.AnalyticsLoadedMsg{
		Snapshot: analytics.Snapshot{GeneratedAt: now},
	})

	if !tui.AnalyticsLoaded(m) {
		t.Error("AnalyticsLoaded = false after AnalyticsLoadedMsg, want true")
	}
	snap := tui.AnalyticsSnapshot(m)
	if snap.GeneratedAt != now {
		t.Errorf("Snapshot.GeneratedAt = %v, want %v", snap.GeneratedAt, now)
	}
}

// TestAnalyticsLoadedMsg_emptyNotStored verifies that an empty
// AnalyticsLoadedMsg (no Snapshot, no Err — used when nothing real
// happened) does not set analyticsLoaded.
func TestAnalyticsLoadedMsg_emptyNotStored(t *testing.T) {
	m := tui.NewModel(&storeRefresher{store: newEmptyStore(t)})

	m, _ = m.Update(tui.AnalyticsLoadedMsg{}) // zero Snapshot, nil Err

	if tui.AnalyticsLoaded(m) {
		t.Error("AnalyticsLoaded = true after empty AnalyticsLoadedMsg, want false")
	}
}

// TestAnalyticsLoadedMsg_errorSurfacesAsNotice verifies that an
// AnalyticsLoadedMsg carrying an Err is surfaced via m.notice AND
// flips analyticsLoaded so the forensics body moves off "loading…".
func TestAnalyticsLoadedMsg_errorSurfacesAsNotice(t *testing.T) {
	m := tui.NewModel(&storeRefresher{store: newEmptyStore(t)})

	m, _ = m.Update(tui.AnalyticsLoadedMsg{Err: errors.New("boom")})

	if !tui.AnalyticsLoaded(m) {
		t.Error("AnalyticsLoaded should flip on error to clear loading state")
	}
	notice := tui.NoticeOf(m)
	if !strings.Contains(stripANSI(notice), "boom") {
		t.Errorf("notice missing error message; got %q", notice)
	}
}

// TestRefreshOnForensics_returnsLoadCmd verifies that pressing r while on
// the forensics tab returns a non-nil tea.Cmd that dispatches
// AnalyticsLoadedMsg (not the ops Refresher).
func TestRefreshOnForensics_returnsLoadCmd(t *testing.T) {
	store := newEmptyStore(t)
	m := tui.NewModel(&storeRefresher{store: store})

	// Switch to forensics (discard the initial load cmd — already tested
	// by TestTabSwitchToForensics_returnsLoadCmd).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})

	// Press r on forensics — should return the analytics loader.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd after r on forensics, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tui.AnalyticsLoadedMsg); !ok {
		t.Errorf("r on forensics: cmd() returned %T, want tui.AnalyticsLoadedMsg", msg)
	}
}

// TestRefreshOnOps_doesNotReturnAnalyticsCmd verifies that pressing r on
// the ops tab still calls Refresh() and does not return an analytics cmd.
func TestRefreshOnOps_doesNotReturnAnalyticsCmd(t *testing.T) {
	rf := &fakeRefresher{store: newEmptyStore(t)}
	m := tui.NewModel(rf)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if rf.calls != 1 {
		t.Errorf("Refresh() called %d times on ops r, want 1", rf.calls)
	}
	// Ops r must not return an analytics load cmd.
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tui.AnalyticsLoadedMsg); ok {
			t.Error("r on ops returned an AnalyticsLoadedMsg cmd, want ops refresh behavior (no cmd)")
		}
	}
}
