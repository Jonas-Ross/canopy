package pr

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// stubRun replaces the runCmd seam for the duration of a test, restoring
// the original on cleanup.
func stubRun(t *testing.T, fn func(ctx context.Context, dir, name string, args ...string) ([]byte, error)) {
	t.Helper()
	orig := SetRunCmd(fn)
	t.Cleanup(func() { SetRunCmd(orig) })
}

// stubLookPath replaces lookPath for the duration of a test. Pass an
// error to simulate "binary not on PATH"; pass nil to simulate the
// happy "binary found" case (the returned path is irrelevant).
func stubLookPath(t *testing.T, err error) {
	t.Helper()
	seamsMu.Lock()
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if err != nil {
			return "", err
		}
		return "/usr/local/bin/" + name, nil
	}
	seamsMu.Unlock()
	t.Cleanup(func() {
		seamsMu.Lock()
		lookPath = orig
		seamsMu.Unlock()
	})
}

const fixtureHappy = `[
  {
    "number": 101,
    "title": "feat: add canopy",
    "headRefName": "feat/canopy",
    "state": "OPEN",
    "isDraft": false,
    "statusCheckRollup": [
      {"status": "COMPLETED", "conclusion": "SUCCESS"},
      {"status": "COMPLETED", "conclusion": "SKIPPED"}
    ],
    "reviewDecision": "APPROVED",
    "mergedAt": "",
    "updatedAt": "2026-05-17T10:30:00Z",
    "url": "https://github.com/jonas/canopy/pull/101"
  },
  {
    "number": 102,
    "title": "wip: pricing",
    "headRefName": "feat/pricing",
    "state": "OPEN",
    "isDraft": true,
    "statusCheckRollup": [
      {"status": "IN_PROGRESS", "conclusion": ""},
      {"status": "COMPLETED", "conclusion": "SUCCESS"}
    ],
    "reviewDecision": "REVIEW_REQUIRED",
    "mergedAt": "",
    "updatedAt": "2026-05-17T09:00:00Z",
    "url": "https://github.com/jonas/canopy/pull/102"
  },
  {
    "number": 99,
    "title": "chore: bump deps",
    "headRefName": "chore/bump-deps",
    "state": "MERGED",
    "isDraft": false,
    "statusCheckRollup": [
      {"status": "COMPLETED", "conclusion": "SUCCESS"}
    ],
    "reviewDecision": "APPROVED",
    "mergedAt": "2026-05-14T12:00:00Z",
    "updatedAt": "2026-05-14T12:00:00Z",
    "url": "https://github.com/jonas/canopy/pull/99"
  }
]`

func TestList_ParsesHappyPath(t *testing.T) {
	stubLookPath(t, nil)
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name != "gh" {
			t.Fatalf("runCmd name=%q, want gh", name)
		}
		if dir != "/tmp/repo" {
			t.Fatalf("runCmd dir=%q, want /tmp/repo", dir)
		}
		// Spot-check the canonical args.
		want := []string{
			"pr", "list",
			"--json", "number,title,state,isDraft,headRefName,statusCheckRollup,reviewDecision,mergedAt,updatedAt,url",
			"--state", "all",
			"--limit", "100",
		}
		if len(args) != len(want) {
			t.Fatalf("runCmd args len=%d want=%d (%v)", len(args), len(want), args)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Fatalf("runCmd args[%d]=%q want=%q", i, args[i], want[i])
			}
		}
		return []byte(fixtureHappy), nil
	})

	prs, err := List(context.Background(), "/tmp/repo")
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(prs) != 3 {
		t.Fatalf("len(prs)=%d, want 3", len(prs))
	}

	// PR 101: open, all-success checks (SUCCESS + SKIPPED), approved.
	got := prs[0]
	if got.Number != 101 || got.Title != "feat: add canopy" || got.HeadBranch != "feat/canopy" {
		t.Errorf("prs[0] identity mismatch: %+v", got)
	}
	if got.State != PRStateOpen || got.IsDraft {
		t.Errorf("prs[0] state mismatch: State=%q IsDraft=%v", got.State, got.IsDraft)
	}
	if got.CIRollup != CISuccess {
		t.Errorf("prs[0] CIRollup=%q, want SUCCESS", got.CIRollup)
	}
	if got.ReviewState != ReviewApproved {
		t.Errorf("prs[0] ReviewState=%q, want APPROVED", got.ReviewState)
	}
	if !got.MergedAt.IsZero() {
		t.Errorf("prs[0] MergedAt=%v, want zero (PR is open)", got.MergedAt)
	}
	wantUpdated := time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)
	if !got.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("prs[0] UpdatedAt=%v, want %v", got.UpdatedAt, wantUpdated)
	}
	if got.URL != "https://github.com/jonas/canopy/pull/101" {
		t.Errorf("prs[0] URL=%q", got.URL)
	}

	// PR 102: draft open, pending check (IN_PROGRESS) — pending wins.
	got = prs[1]
	if got.Number != 102 || !got.IsDraft {
		t.Errorf("prs[1] identity/draft mismatch: %+v", got)
	}
	if got.State != PRStateOpen {
		t.Errorf("prs[1] State=%q, want OPEN", got.State)
	}
	if got.CIRollup != CIPending {
		t.Errorf("prs[1] CIRollup=%q, want PENDING", got.CIRollup)
	}
	if got.ReviewState != ReviewRequired {
		t.Errorf("prs[1] ReviewState=%q", got.ReviewState)
	}

	// PR 99: merged.
	got = prs[2]
	if got.State != PRStateMerged {
		t.Errorf("prs[2] State=%q, want MERGED", got.State)
	}
	wantMerged := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	if !got.MergedAt.Equal(wantMerged) {
		t.Errorf("prs[2] MergedAt=%v, want %v", got.MergedAt, wantMerged)
	}
}

func TestList_EmptyResult(t *testing.T) {
	stubLookPath(t, nil)
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		return []byte("[]"), nil
	})
	prs, err := List(context.Background(), "/tmp/repo")
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("len(prs)=%d, want 0", len(prs))
	}
}

// SetLookPath is the public seam the demo subcommand uses to skip the
// "gh on PATH" check on hosts without gh installed. Regression: without
// it, swapping SetRunCmd alone is not enough — List returns ErrNoGH
// before the run seam fires and the canned PR fixture never lands.
func TestSetLookPath_AllowsRunCmdToFireOnHostWithoutGH(t *testing.T) {
	// Public-seam flow: swap LookPath with SetLookPath; without restoring
	// here we'd leak into the next test, hence the explicit restore.
	origLook := SetLookPath(func(name string) (string, error) {
		return "/fake/bin/" + name, nil
	})
	t.Cleanup(func() { SetLookPath(origLook) })

	called := false
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		called = true
		return []byte(fixtureHappy), nil
	})

	prs, err := List(context.Background(), "/tmp/repo")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !called {
		t.Fatalf("runCmd was not called; LookPath stub did not take effect")
	}
	if len(prs) == 0 {
		t.Fatalf("len(prs)=0, want fixtureHappy entries")
	}
}

func TestList_GhNotInstalled(t *testing.T) {
	stubLookPath(t, exec.ErrNotFound)
	// runCmd must not be called when gh is missing; install a stub
	// that fails the test if it is.
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		t.Fatalf("runCmd called despite gh missing from PATH")
		return nil, nil
	})
	_, err := List(context.Background(), "/tmp/repo")
	if !errors.Is(err, ErrNoGH) {
		t.Fatalf("List err=%v, want ErrNoGH", err)
	}
}

func TestList_NotAuthed(t *testing.T) {
	stubLookPath(t, nil)
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		// Plain error carrying the signal phrase. classifyGHErr also
		// inspects *exec.ExitError.Stderr; both paths must match the
		// same probe set, and the plain-error path is what's easy to
		// stub from a test.
		return nil, errors.New("gh: not logged in to github.com")
	})
	_, err := List(context.Background(), "/tmp/repo")
	if !errors.Is(err, ErrNotAuthed) {
		t.Fatalf("List err=%v, want ErrNotAuthed", err)
	}
}

func TestList_OtherFailure(t *testing.T) {
	stubLookPath(t, nil)
	boom := errors.New("gh: HTTP 500 from api.github.com")
	stubRun(t, func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		return nil, boom
	})
	_, err := List(context.Background(), "/tmp/repo")
	if err == nil {
		t.Fatalf("List err=nil, want non-nil")
	}
	if errors.Is(err, ErrNoGH) || errors.Is(err, ErrNotAuthed) {
		t.Fatalf("List err=%v, want plain wrapped error", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("List err=%v, want wrap of original (%v)", err, boom)
	}
}

func TestList_CIRollup_AllSuccess(t *testing.T) {
	got := rollupCI([]ghCheckRoll{
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Status: "COMPLETED", Conclusion: "SKIPPED"},
	})
	if got != CISuccess {
		t.Fatalf("rollupCI=%q, want SUCCESS", got)
	}
}

func TestList_CIRollup_AnyFailure(t *testing.T) {
	got := rollupCI([]ghCheckRoll{
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Status: "COMPLETED", Conclusion: "FAILURE"},
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
	})
	if got != CIFailure {
		t.Fatalf("rollupCI=%q, want FAILURE", got)
	}
}

func TestList_CIRollup_PendingShortCircuits(t *testing.T) {
	// One pending, one failure. Pending must win because a pending
	// check can still flip and the truthful summary is "we don't
	// know yet."
	got := rollupCI([]ghCheckRoll{
		{Status: "COMPLETED", Conclusion: "FAILURE"},
		{Status: "IN_PROGRESS", Conclusion: ""},
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
	})
	if got != CIPending {
		t.Fatalf("rollupCI=%q, want PENDING", got)
	}

	// QUEUED also counts as pending.
	got = rollupCI([]ghCheckRoll{
		{Status: "QUEUED", Conclusion: ""},
		{Status: "COMPLETED", Conclusion: "FAILURE"},
	})
	if got != CIPending {
		t.Fatalf("rollupCI(QUEUED)=%q, want PENDING", got)
	}

	// PENDING status string variant.
	got = rollupCI([]ghCheckRoll{
		{Status: "PENDING", Conclusion: ""},
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
	})
	if got != CIPending {
		t.Fatalf("rollupCI(PENDING status)=%q, want PENDING", got)
	}
}

func TestList_CIRollup_EmptyChecks(t *testing.T) {
	if got := rollupCI(nil); got != "" {
		t.Fatalf("rollupCI(nil)=%q, want empty", got)
	}
	if got := rollupCI([]ghCheckRoll{}); got != "" {
		t.Fatalf("rollupCI(empty)=%q, want empty", got)
	}
}
