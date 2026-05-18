package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/tui"
)

// runIfCmd drains a returned tea.Cmd so the test can observe side effects
// (e.g., m.refresher.Refresh being called via refreshCmd).
func runIfCmd(cmd tea.Cmd) {
	if cmd != nil {
		cmd()
	}
}

// Tests for the operation-result message handlers in tui/tui.go: every
// *Msg switch arm has at least one positive and one negative assertion.

func TestUpdate_WorktreeRemovedMsg_SuccessNoticesAndRefreshes(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	var cmd tea.Cmd
	m, cmd = m.Update(tui.MakeWorktreeRemovedMsg("/repo/wt-a", nil))
	runIfCmd(cmd)

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "pruned") || !strings.Contains(notice, "/repo/wt-a") {
		t.Errorf("notice = %q, want 'pruned' + path", notice)
	}
	if rf.calls != 1 {
		t.Errorf("Refresh calls = %d, want 1 (success must trigger refresh)", rf.calls)
	}
}

func TestUpdate_WorktreeRemovedMsg_ErrorShowsErrorNotice(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tui.MakeWorktreeRemovedMsg("/repo/wt-a", errors.New("worktree locked")))

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "prune failed") || !strings.Contains(notice, "worktree locked") {
		t.Errorf("error notice = %q, want 'prune failed: worktree locked'", notice)
	}
	if rf.calls != 0 {
		t.Errorf("Refresh calls = %d on error, want 0", rf.calls)
	}
}

func TestUpdate_WorktreeCreatedMsg_SuccessAndError(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)

	var cmd tea.Cmd
	m, cmd = m.Update(tui.MakeWorktreeCreatedMsg("feat/x", "/repo/.worktrees/feat+x", nil))
	runIfCmd(cmd)
	if notice := stripANSI(tui.NoticeOf(m)); !strings.Contains(notice, "created feat/x") {
		t.Errorf("success notice = %q, want 'created feat/x …'", notice)
	}
	if rf.calls != 1 {
		t.Errorf("Refresh calls after success = %d, want 1", rf.calls)
	}

	m, cmd = m.Update(tui.MakeWorktreeCreatedMsg("feat/y", "", errors.New("already exists")))
	runIfCmd(cmd)
	if notice := stripANSI(tui.NoticeOf(m)); !strings.Contains(notice, "create failed") || !strings.Contains(notice, "already exists") {
		t.Errorf("error notice = %q, want 'create failed: already exists'", notice)
	}
	if rf.calls != 1 {
		t.Errorf("Refresh calls after error = %d, want still 1 (no refresh on error)", rf.calls)
	}
}

func TestUpdate_ProcsKilledMsg_FullSuccess(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	var cmd tea.Cmd
	m, cmd = m.Update(tui.MakeProcsKilledMsg(3, nil))
	runIfCmd(cmd)

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "sent SIGTERM to 3 procs") {
		t.Errorf("notice = %q, want 'sent SIGTERM to 3 procs'", notice)
	}
	if rf.calls != 1 {
		t.Errorf("Refresh calls = %d, want 1", rf.calls)
	}
}

func TestUpdate_ProcsKilledMsg_PartialError(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	// count > 0 with non-nil err means some succeeded, some failed.
	var cmd tea.Cmd
	m, cmd = m.Update(tui.MakeProcsKilledMsg(2, errors.New("permission denied")))
	runIfCmd(cmd)

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "sent SIGTERM to 2 procs") || !strings.Contains(notice, "some errored") {
		t.Errorf("partial-error notice = %q, want '2 procs (some errored)'", notice)
	}
	if rf.calls != 1 {
		t.Errorf("Refresh calls = %d on partial success, want 1", rf.calls)
	}
}

func TestUpdate_ProcsKilledMsg_FullFailure(t *testing.T) {
	rf := &fakeRefresher{}
	m := tui.NewModel(rf)
	m, _ = m.Update(tui.MakeProcsKilledMsg(0, errors.New("no such process")))

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "kill failed") || !strings.Contains(notice, "no such process") {
		t.Errorf("error notice = %q, want 'kill failed: no such process'", notice)
	}
}

func TestUpdate_PROpenedMsg_SuccessDismissesNotice(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	// Seed the "opening …" notice that handleOpenPR would set before its
	// returned cmd fires.
	state := aggregator.WorktreeState{
		Worktree: newBaseWorktree("/repo/wt-a", "feat/a"),
		PR:       &pr.PR{Number: 1, URL: "https://example.invalid/pull/1", State: "OPEN"},
	}
	m, _ = m.Update(tui.UpdateMsg(aggregator.Update{Worktree: "/repo/wt-a", State: state}))
	m, _ = m.Update(sendKey('p')) // sets "opening https://…" notice

	if got := stripANSI(tui.NoticeOf(m)); !strings.Contains(got, "opening ") {
		t.Fatalf("pre-condition: notice = %q, want 'opening …' before resolving cmd", got)
	}

	m, _ = m.Update(tui.MakePROpenedMsg(nil))
	if notice := tui.NoticeOf(m); notice != "" {
		t.Errorf("notice after successful prOpenedMsg = %q, want empty (dismissed)", notice)
	}
}

func TestUpdate_PROpenedMsg_ErrorShowsErrorNotice(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tui.MakePROpenedMsg(errors.New("xdg-open not found")))

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "open PR failed") || !strings.Contains(notice, "xdg-open not found") {
		t.Errorf("error notice = %q, want 'open PR failed: …'", notice)
	}
}

func TestUpdate_ShellDroppedMsg_SuccessSilent(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	// nil error == clean exit from the dropped shell; no notice.
	m, _ = m.Update(tui.MakeShellDroppedMsg(nil))
	if notice := tui.NoticeOf(m); notice != "" {
		t.Errorf("notice after clean shell exit = %q, want empty", notice)
	}
}

func TestUpdate_ShellDroppedMsg_ErrorShowsErrorNotice(t *testing.T) {
	m := tui.NewModel(&fakeRefresher{})
	m, _ = m.Update(tui.MakeShellDroppedMsg(errors.New("exit status 1")))

	notice := stripANSI(tui.NoticeOf(m))
	if !strings.Contains(notice, "shell exited with error") || !strings.Contains(notice, "exit status 1") {
		t.Errorf("error notice = %q, want 'shell exited with error: …'", notice)
	}
}

